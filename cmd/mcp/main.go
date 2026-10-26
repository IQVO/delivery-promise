// Command mcp is the composition root for the delivery-promise MCP server
// (docs/adr/0007-mcp-server-adoption.md): it wires env config to the SAME
// usecases.Service the REST adapter of cmd/api drives, and exposes its five
// read queries as MCP tools over Streamable HTTP (and nothing else: no
// stdio, no SSE). It is a second, independent deployable alongside cmd/api.
//
// The surface is READ-ONLY: only the read queries are reachable (the MCP
// adapter's Reads interface has no write method), so this binary can never
// move an aggregate and never inserts into the outbox. It never starts the
// outbox relay, never runs the inbound consumer or the clock-driven sweeper
// and never dials Kafka (no KAFKA_BROKERS, no consumer group): those run in
// cmd/api only. It makes no REST or MCP call to any sibling context.
//
// There is NO auth of any kind (fleet-wide revert 2026-09-11): no keys, no
// bearer checks. Access control is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	MCP_ADDR                 listen address (default :8090)
//	DATABASE_URL             REQUIRED. Postgres DSN (the service database cmd/api owns)
//	MIGRATIONS_DATABASE_URL  direct DSN for the boot migrations (default DATABASE_URL)
//	LOG_LEVEL                debug | info | warn | error (default info)
//	OTEL_SERVICE_NAME        service.name of spans and metrics (default delivery-promise-mcp)
//	OTEL_EXPORTER_OTLP_ENDPOINT  Collector OTLP/gRPC endpoint (default telemetry.DefaultEndpoint); export never blocks
//	SERVICE_VERSION          service.version, reported in the boot log line too
//	ENVIRONMENT              deployment.environment.name, reported in the boot log line too
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundmcp "github.com/claudioed/delivery-promise/internal/adapters/inbound/mcp"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/clock"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/ids"
	outboundkafka "github.com/claudioed/delivery-promise/internal/adapters/outbound/kafka"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/telemetry"
	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/bootretry"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// errNoDatabase is the boot error of a missing DATABASE_URL: this binary has
// no in-memory mode.
var errNoDatabase = errors.New("DATABASE_URL is required: this service runs on real Postgres, there is no in-memory mode")

func main() {
	if err := run(); err != nil {
		slog.Error("mcp server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	// Telemetry comes up before the router is built (otelchimetric binds the
	// global MeterProvider at construction) and is flushed last.
	tel := loadTelemetryEnv(os.Getenv)
	flushTelemetry, err := startTelemetry(logger, tel)
	if err != nil {
		return err
	}
	defer flushTelemetry()

	databaseURL := os.Getenv("DATABASE_URL")
	deps, closeAdapters, err := buildDeps(context.Background(), logger, databaseURL, getenv("MIGRATIONS_DATABASE_URL", databaseURL))
	if err != nil {
		return err
	}
	defer closeAdapters()

	srv := &http.Server{
		Addr:              getenv("MCP_ADDR", ":8090"),
		Handler:           newRouter(inboundmcp.Handler(inboundmcp.NewServer(deps)), logger, tel.serviceName),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return serveMCP(logger, srv)
}

// buildDeps migrates the database, connects a pgx pool and wires the read
// use cases over the Postgres repositories -- the SAME usecases.Service
// cmd/api drives, so both surfaces answer from one application layer. The
// returned close func releases the pool. An empty databaseURL is a boot
// error: there is no fallback runtime.
//
// Like cmd/api this binary runs the idempotent embedded migrations on start,
// so it can boot against a fresh database before cmd/api has; golang-
// migrate's advisory lock makes concurrent starts safe.
// migrationsDatabaseURL is used ONLY for that step (a DIRECT Postgres DSN
// where DATABASE_URL points at PgBouncer, which cannot honour the
// session-scoped advisory lock); the runtime pool always uses databaseURL.
func buildDeps(ctx context.Context, logger *slog.Logger, databaseURL, migrationsDatabaseURL string) (inboundmcp.Deps, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		return inboundmcp.Deps{}, noop, errNoDatabase
	}
	// The first outbound dial of an injected pod can be reset (Istio native
	// sidecars), so migrations and the first ping retry with backoff; on
	// exhaustion the process refuses to boot.
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error {
		return postgres.RunMigrations(migrationsDatabaseURL)
	}); err != nil {
		return inboundmcp.Deps{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return inboundmcp.Deps{}, noop, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return inboundmcp.Deps{}, noop, err
	}
	logger.Info("mcp server wired to the postgres repositories",
		"service_version", os.Getenv("SERVICE_VERSION"), "environment", os.Getenv("ENVIRONMENT"))
	return inboundmcp.Deps{Reads: buildService(pool, logger)}, pool.Close, nil
}

// buildService wires the Postgres adapters into the use cases, exactly like
// cmd/api's buildService: one application layer serves the REST adapter and
// the MCP adapter alike. The outbox encoder only marshals CloudEvents (it
// dials nothing); a read can never reach it.
func buildService(pool *pgxpool.Pool, logger *slog.Logger) *usecases.Service {
	return buildServiceWithClock(pool, logger, clock.System{})
}

// buildServiceWithClock is buildService with an injectable clock (the
// integration test seeds the worked example of 2026-10-22 against the fixed
// instant it was authored for).
func buildServiceWithClock(pool *pgxpool.Pool, logger *slog.Logger, clk ports.Clock) *usecases.Service {
	return usecases.New(usecases.Deps{
		UoW:       postgres.NewUnitOfWork(pool),
		Offers:    postgres.NewOfferRepository(pool),
		Promises:  postgres.NewPromiseRepository(pool),
		Books:     postgres.NewBookRepository(pool),
		Policies:  postgres.NewPolicyRepository(pool),
		Network:   postgres.NewNetworkViews(pool),
		Shipments: postgres.NewShipmentViews(pool),
		Outbox:    postgres.NewOutbox(pool, outboundkafka.NewEncoder()),
		Inbox:     postgres.NewInbox(pool),
		Clock:     clk,
		IDs:       ids.UUID{},
		Logger:    logger,
	})
}

// serveMCP runs srv until it fails or SIGINT/SIGTERM arrives, then drains it
// gracefully.
func serveMCP(logger *slog.Logger, srv *http.Server) error {
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("mcp server listening (Streamable HTTP)", "addr", srv.Addr)
		serverErr <- srv.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// newLogger builds the process-wide JSON logger. LOG_LEVEL maps
// debug|info|warn|warning|error (case-insensitive; "warning" is accepted
// like "warn" fleet-wide), defaulting to Info. The handler is wrapped so a
// record written with a span-carrying context also carries trace_id/span_id.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
