package main

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/delivery-promise/internal/adapters/inbound/http"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/clock"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/telemetry"
	"github.com/claudioed/delivery-promise/internal/application/requestfacts"
)

// The request-level coverage fact (ADR 0032): OFF unless
// DELIVERY_REQUEST_FACTS_ENABLED is true, best effort, and sized by two optional
// variables.
const (
	envRequestFactsEnabled       = "DELIVERY_REQUEST_FACTS_ENABLED"
	envRequestFactsQueueSize     = "DELIVERY_REQUEST_FACTS_QUEUE_SIZE"
	envRequestFactsFlushInterval = "DELIVERY_REQUEST_FACTS_FLUSH_INTERVAL"
)

// requestFactsEnabled reads DELIVERY_REQUEST_FACTS_ENABLED. Unset or empty is
// false (nothing changes on deploy); a value that is not a boolean warns and is
// false too, so a typo never switches an extra write on.
func requestFactsEnabled(raw string, logger *slog.Logger) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		logger.Warn("invalid DELIVERY_REQUEST_FACTS_ENABLED; the request facts stay off", "value", raw)
		return false
	}
	return on
}

// requestFactsConfig reads the queue capacity (DELIVERY_REQUEST_FACTS_QUEUE_SIZE,
// default 1000, at most 100000) and the flush interval
// (DELIVERY_REQUEST_FACTS_FLUSH_INTERVAL, Go duration or seconds, default 1s). An
// invalid or out-of-range value warns and takes the default.
func requestFactsConfig(env func(string) string, logger *slog.Logger) requestfacts.Config {
	cfg := requestfacts.Config{Capacity: requestfacts.DefaultCapacity}
	if raw := strings.TrimSpace(env(envRequestFactsQueueSize)); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > requestfacts.MaxCapacity {
			logger.Warn("invalid DELIVERY_REQUEST_FACTS_QUEUE_SIZE; using the default", "value", raw, "default", requestfacts.DefaultCapacity, "max", requestfacts.MaxCapacity)
		} else {
			cfg.Capacity = n
		}
	}
	cfg.FlushInterval = parseDuration(envRequestFactsFlushInterval, env(envRequestFactsFlushInterval), requestfacts.DefaultFlushInterval, logger)
	return cfg
}

// startRequestFacts wires the best-effort path when it is switched on: the
// recorder the REST adapter calls after a response, and the single background
// worker that drains its queue into the outbox. It returns a nil recorder (and
// no worker) when the feature is off, so the routes are the ones without it.
func startRequestFacts(pool *pgxpool.Pool, outbox *postgres.Outbox, env func(string) string, logger *slog.Logger) (inboundhttp.FactRecorder, *worker, error) {
	if !requestFactsEnabled(env(envRequestFactsEnabled), logger) {
		return nil, nil, nil
	}
	metrics, err := telemetry.NewRequestFactMetrics()
	if err != nil {
		return nil, nil, err
	}
	cfg := requestFactsConfig(env, logger)
	writer := requestfacts.OutboxWriter{UoW: postgres.NewUnitOfWork(pool), Outbox: outbox, NewID: uuid.NewString}
	queue := requestfacts.NewQueue(writer, metrics, logger, cfg)
	w := startWorker("request-facts", logger, func(ctx context.Context) error {
		logger.Info("request facts running (best effort)", "queue_capacity", queue.Capacity(), "flush_interval", cfg.FlushInterval)
		return queue.Run(ctx)
	})
	return requestfacts.NewRecorder(queue, metrics, clock.System{}.Now), w, nil
}
