// Command api is the delivery-promise composition root: it wires the
// environment into the adapters, the adapters into the use cases and the use
// cases into the HTTP router, the Kafka consumer, the outbox relay and the
// clock-driven sweeper, all on real Postgres and real Kafka. There is no
// in-memory mode, no log sink and no consumer that is off.
//
// Environment:
//
//	HTTP_ADDR                     listen address (default :8080)
//	DATABASE_URL                  Postgres DSN (REQUIRED)
//	MIGRATIONS_DATABASE_URL       direct DSN for the boot migrations (default DATABASE_URL)
//	KAFKA_BROKERS                 comma-separated brokers (REQUIRED: relay, consumer, /readyz)
//	DELIVERY_PROMISE_CONSUMER_GROUP stable consumer group of the inbound consumer (REQUIRED)
//	OUTBOX_RELAY_INTERVAL         relay poll interval (Go duration or seconds, default 1s)
//	OUTBOX_MAX_ATTEMPTS           failures before the relay excludes a row (default 20, 0 = unlimited)
//	SWEEP_INTERVAL                clock-driven sweep interval (default 15s)
//	SHUTDOWN_DRAIN_DELAY          wait after flipping /readyz before closing (default 5s)
//	LOG_LEVEL                     debug|info|warn|error (default info)
//
// GET /readyz is 200 only while Postgres answers a ping AND a Kafka broker
// answers a metadata request (verdict cached 3s, bounded 2s); /healthz is
// liveness only. The process exits non-zero when the relay, the consumer or
// the sweeper returns on its own, so the orchestrator restarts it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/delivery-promise/internal/adapters/inbound/http"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/clock"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/ids"
	outboundkafka "github.com/claudioed/delivery-promise/internal/adapters/outbound/kafka"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/bootretry"
)

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

// run boots the service and serves until SIGINT/SIGTERM or until ctx is
// cancelled (the integration test stops the real entry point that way).
func run(ctx context.Context) error {
	logger := newLogger(orDefault(os.Getenv("LOG_LEVEL"), "info"))
	slog.SetDefault(logger)

	// Misconfiguration is refused before anything is opened or bound.
	rt, err := loadRuntime(os.Getenv)
	if err != nil {
		return err
	}
	pool, err := openPostgres(ctx, rt, logger)
	if err != nil {
		return err
	}
	// pool.Close is deferred FIRST so it runs LAST, after the HTTP drain, the
	// consumer, the sweeper and the relay have stopped.
	defer pool.Close()

	outbox := postgres.NewOutbox(pool, outboundkafka.NewEncoder())
	svc := buildService(pool, outbox, logger)
	readiness := inboundhttp.NewReadiness(inboundhttp.DefaultReadinessTTL, inboundhttp.DefaultReadinessTimeout,
		inboundhttp.Dependency{Name: "postgres", Check: pool.Ping},
		inboundhttp.Dependency{Name: "kafka", Check: outboundkafka.NewBrokerCheck(rt.brokers).Check},
	)
	router := inboundhttp.NewRouter(&inboundhttp.Server{
		API:         svc,
		Readiness:   readiness,
		Idempotency: inboundhttp.RequireIdempotencyKey(pool, postgres.BindTx),
	})
	httpServer := &http.Server{
		Addr:              rt.httpAddr,
		Handler:           inboundhttp.AccessLog(logger)(inboundhttp.Recoverer(router)),
		ReadHeaderTimeout: 5 * time.Second,
	}

	consumer, closeConsumer := startConsumer(svc, rt, logger)
	sweep, closeSweeper := startSweeper(svc, os.Getenv, logger)
	relay, closeRelay := startOutboxRelay(outbox, rt, os.Getenv, logger)
	return serve(ctx, logger, httpServer, readiness, []*worker{consumer, sweep}, func() { closeConsumer(); closeSweeper() }, relay, closeRelay)
}

// openPostgres runs the embedded migrations, then opens and pings the pool. The
// first outbound dial of an injected pod can be reset, so both retry with
// backoff; on exhaustion the LAST error is returned and the process refuses to
// boot.
func openPostgres(ctx context.Context, rt runtimeEnv, logger *slog.Logger) (*pgxpool.Pool, error) {
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error { return postgres.RunMigrations(rt.migrationsURL) }); err != nil {
		return nil, err
	}
	pool, err := postgres.NewPool(ctx, rt.databaseURL)
	if err != nil {
		return nil, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return nil, err
	}
	logger.Info("postgres adapters configured")
	return pool, nil
}

// buildService wires the Postgres adapters into the use cases.
func buildService(pool *pgxpool.Pool, outbox *postgres.Outbox, logger *slog.Logger) *usecases.Service {
	network := postgres.NewNetworkViews(pool)
	return usecases.New(usecases.Deps{
		UoW:       postgres.NewUnitOfWork(pool),
		Offers:    postgres.NewOfferRepository(pool),
		Promises:  postgres.NewPromiseRepository(pool),
		Books:     postgres.NewBookRepository(pool),
		Policies:  postgres.NewPolicyRepository(pool),
		Network:   network,
		Shipments: postgres.NewShipmentViews(pool),
		Outbox:    outbox,
		Inbox:     postgres.NewInbox(pool),
		Clock:     clock.System{},
		IDs:       ids.UUID{},
		Logger:    logger,
	})
}

// serve runs httpServer until SIGINT/SIGTERM (or a listen error), then shuts
// down in the fleet order: (1) /readyz flips to 503, (2) wait the drain delay,
// (3) drain HTTP, (4) stop and await the consumer and sweeper, (5) stop and
// await the relay LAST so nothing committed above is stranded.
func serve(ctx context.Context, logger *slog.Logger, httpServer *http.Server, readiness *inboundhttp.Readiness,
	workers []*worker, closeWorkers func(), relay *worker, closeRelay func(),
) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	died := watchWorkers(append([]*worker{relay}, workers...)...)
	select {
	case err := <-errCh:
		closeWorkers()
		closeRelay()
		return err
	case name := <-died:
		// A worker returned on its own: the service cannot run without it and
		// must not sit "alive" with a dead worker.
		closeWorkers()
		closeRelay()
		return fmt.Errorf("%s stopped unexpectedly: exiting so the orchestrator restarts the service", name)
	case <-sigCtx.Done():
	}

	readiness.SetNotReady()
	if delay := shutdownDrainDelay(os.Getenv(envShutdownDrainDelay), logger); delay > 0 {
		logger.Info("shutdown: readiness flipped to not-ready; waiting for traffic to drain", "drain_delay", delay.String())
		time.Sleep(delay)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := httpServer.Shutdown(shutdownCtx)

	closeWorkers()
	for _, w := range workers {
		await(logger, w)
	}
	closeRelay()
	await(logger, relay)
	return err
}

// watchWorkers returns a channel that receives the name of every worker that
// returns. A value read while the service is serving means a worker died.
func watchWorkers(workers ...*worker) <-chan string {
	died := make(chan string, len(workers))
	for _, w := range workers {
		go func() {
			<-w.done
			died <- w.name
		}()
	}
	return died
}

// await waits (bounded) for w to return.
func await(logger *slog.Logger, w *worker) {
	select {
	case <-w.done:
	case <-time.After(workerDrainTimeout):
		logger.Warn("worker did not stop before the shutdown drain deadline", "worker", w.name)
	}
}
