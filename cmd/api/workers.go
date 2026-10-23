package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	inboundkafka "github.com/claudioed/delivery-promise/internal/adapters/inbound/kafka"
	outboundkafka "github.com/claudioed/delivery-promise/internal/adapters/outbound/kafka"
	outboxrelay "github.com/claudioed/delivery-promise/internal/adapters/outbound/outbox"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

// worker is a started background loop and the channel closed once it returned.
type worker struct {
	name string
	done chan struct{}
	stop context.CancelFunc
}

// startWorker runs fn until it returns; context.Canceled is the clean exit.
func startWorker(name string, logger *slog.Logger, fn func(ctx context.Context) error) *worker {
	ctx, stop := context.WithCancel(context.Background())
	w := &worker{name: name, done: make(chan struct{}), stop: stop}
	go func() {
		defer close(w.done)
		if err := fn(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("worker stopped", "worker", name, "error", err)
		}
	}()
	return w
}

// startConsumer starts the single consumer of the eight integration topics.
// The reader dials lazily inside Run, so a broker outage never blocks boot.
func startConsumer(svc *usecases.Service, rt runtimeEnv, logger *slog.Logger) (*worker, func()) {
	c := inboundkafka.NewConsumer("delivery-promise", rt.brokers, inboundkafka.Topics(), rt.group, inboundkafka.Handlers(svc), logger)
	w := startWorker("consumer", logger, func(ctx context.Context) error {
		logger.Info("consumer running", "topics", c.Topics, "group_id", rt.group, "brokers", rt.brokers)
		return c.Run(ctx)
	})
	return w, func() { w.stop(); _ = c.Close() }
}

// startOutboxRelay starts the always-on relay over the Kafka sink. Kafka is
// dialled lazily on the first send, never here.
func startOutboxRelay(store outboxrelay.Store, rt runtimeEnv, env func(string) string, logger *slog.Logger) (*worker, func()) {
	writer := outboundkafka.NewWriter(joinBrokers(rt.brokers))
	interval := parseDuration(envOutboxInterval, env(envOutboxInterval), outboxrelay.DefaultInterval, logger)
	maxAttempts := outboxMaxAttempts(env(envOutboxMaxAttempts), logger)
	relay := outboxrelay.NewOutboxRelay(store, outboundkafka.NewRelaySink(writer), logger,
		outboxrelay.WithInterval(interval), outboxrelay.WithMaxAttempts(maxAttempts))
	w := startWorker("outbox-relay", logger, func(ctx context.Context) error {
		logger.Info("outbox relay running", "interval", interval, "max_attempts", maxAttempts)
		return relay.Run(ctx)
	})
	return w, func() {
		w.stop()
		<-w.done
		if err := writer.Close(); err != nil {
			logger.Error("kafka writer close failed", "error", err)
		}
	}
}

// sweeper is the clock-driven part of the model: it expires offers, releases
// expired holds, closes books at their cutoff and re-promises at-risk promises.
type sweeper interface {
	ExpireOffers(ctx context.Context, limit int) (int, error)
	ReleaseExpiredHolds(ctx context.Context, limit int) (int, error)
	CloseBooks(ctx context.Context, limit int) (int, error)
	RepriceAtRisk(ctx context.Context, limit int) (int, error)
}

// sweepOnce runs every clock-driven pass once and logs the failures; one failing
// pass never stops the others.
func sweepOnce(ctx context.Context, s sweeper, logger *slog.Logger) {
	passes := []struct {
		name string
		run  func(context.Context, int) (int, error)
	}{
		{"expire offers", s.ExpireOffers},
		{"release expired holds", s.ReleaseExpiredHolds},
		{"close books", s.CloseBooks},
		{"re-promise at-risk promises", s.RepriceAtRisk},
	}
	for _, p := range passes {
		n, err := p.run(ctx, defaultSweepBatch)
		switch {
		case err != nil && ctx.Err() == nil:
			logger.Error("sweep pass failed", "pass", p.name, "error", err)
		case n > 0:
			logger.Info("sweep pass", "pass", p.name, "count", n)
		}
	}
}

// runSweeper runs sweepOnce every interval until ctx is cancelled.
func runSweeper(ctx context.Context, s sweeper, interval time.Duration, logger *slog.Logger) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			sweepOnce(ctx, s, logger)
		}
	}
}

func startSweeper(s sweeper, env func(string) string, logger *slog.Logger) (*worker, func()) {
	interval := parseDuration(envSweepInterval, env(envSweepInterval), defaultSweepInterval, logger)
	w := startWorker("sweeper", logger, func(ctx context.Context) error {
		logger.Info("sweeper running", "interval", interval)
		return runSweeper(ctx, s, interval, logger)
	})
	return w, w.stop
}

func joinBrokers(brokers []string) string {
	out := ""
	for i, b := range brokers {
		if i > 0 {
			out += ","
		}
		out += b
	}
	return out
}
