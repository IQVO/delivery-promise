package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	outboxrelay "github.com/claudioed/delivery-promise/internal/adapters/outbound/outbox"
)

// Boot errors for the variables the service cannot run without. Each message
// names the variable and is raised before the listener binds.
var (
	errMissingDatabaseURL   = errors.New("DATABASE_URL is required: this service runs on real Postgres, there is no in-memory mode")
	errMissingBrokers       = errors.New("KAFKA_BROKERS is required: this service runs on real Kafka (outbox relay, consumer, /readyz)")
	errMissingConsumerGroup = errors.New("DELIVERY_PROMISE_CONSUMER_GROUP is required: the consumer of the eight producing contexts always runs on a stable group")
)

// Defaults of the optional variables.
const (
	defaultHTTPAddr       = ":8080"
	defaultSweepInterval  = 15 * time.Second
	defaultSweepBatch     = 100
	defaultShutdownDrain  = 5 * time.Second
	shutdownTimeout       = 10 * time.Second
	workerDrainTimeout    = 10 * time.Second
	envConsumerGroup      = "DELIVERY_PROMISE_CONSUMER_GROUP"
	envOutboxInterval     = "OUTBOX_RELAY_INTERVAL"
	envOutboxMaxAttempts  = "OUTBOX_MAX_ATTEMPTS"
	envSweepInterval      = "SWEEP_INTERVAL"
	envShutdownDrainDelay = "SHUTDOWN_DRAIN_DELAY"
)

// runtimeEnv is the validated environment that selects the real runtime.
type runtimeEnv struct {
	databaseURL   string
	migrationsURL string
	brokers       []string
	group         string
	httpAddr      string
}

// loadRuntime validates the environment before anything is opened or bound:
// DATABASE_URL, KAFKA_BROKERS and DELIVERY_PROMISE_CONSUMER_GROUP are
// mandatory and each error names its variable. EVENT_PUBLISHER may only be
// kafka (the log sink does not exist).
func loadRuntime(env func(string) string) (runtimeEnv, error) {
	rt := runtimeEnv{databaseURL: env("DATABASE_URL")}
	if rt.databaseURL == "" {
		return runtimeEnv{}, errMissingDatabaseURL
	}
	if rt.brokers = splitBrokers(env("KAFKA_BROKERS")); len(rt.brokers) == 0 {
		return runtimeEnv{}, errMissingBrokers
	}
	if rt.group = strings.TrimSpace(env(envConsumerGroup)); rt.group == "" {
		return runtimeEnv{}, errMissingConsumerGroup
	}
	if v := env("EVENT_PUBLISHER"); v != "" && v != "kafka" {
		return runtimeEnv{}, fmt.Errorf("EVENT_PUBLISHER=%s is not supported: this service publishes to real Kafka", v)
	}
	rt.migrationsURL = orDefault(env("MIGRATIONS_DATABASE_URL"), rt.databaseURL)
	rt.httpAddr = orDefault(env("HTTP_ADDR"), defaultHTTPAddr)
	return rt, nil
}

func orDefault(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// splitBrokers splits KAFKA_BROKERS on commas, trimming blanks.
func splitBrokers(raw string) []string {
	var out []string
	for _, b := range strings.Split(raw, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}

// parseDuration reads a Go duration or a plain number of seconds; unset,
// malformed or non-positive values give the fallback (and a warning).
func parseDuration(name, raw string, fallback time.Duration, logger *slog.Logger) time.Duration {
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		if secs, serr := strconv.ParseFloat(raw, 64); serr == nil {
			d, err = time.Duration(secs*float64(time.Second)), nil
		}
	}
	if err != nil || d <= 0 {
		logger.Warn("invalid duration; using the default", "variable", name, "value", raw, "default", fallback)
		return fallback
	}
	return d
}

// outboxMaxAttempts reads OUTBOX_MAX_ATTEMPTS: how many times an outbox row may
// fail before the relay excludes it. Unset = the fleet default (20); 0 =
// unlimited; negative or unparsable values warn and use the default, so a typo
// never silently removes the cap.
func outboxMaxAttempts(raw string, logger *slog.Logger) int {
	if raw == "" {
		return outboxrelay.DefaultMaxAttempts
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		logger.Warn("invalid OUTBOX_MAX_ATTEMPTS; using the default", "value", raw, "default", outboxrelay.DefaultMaxAttempts)
		return outboxrelay.DefaultMaxAttempts
	}
	return n
}

// shutdownDrainDelay reads SHUTDOWN_DRAIN_DELAY ("0" disables; unset, negative
// or unparsable = the default).
func shutdownDrainDelay(raw string, logger *slog.Logger) time.Duration {
	if raw == "" {
		return defaultShutdownDrain
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		logger.Warn("ignoring invalid SHUTDOWN_DRAIN_DELAY", "value", raw, "default", defaultShutdownDrain.String())
		return defaultShutdownDrain
	}
	return d
}

// newLogger builds the JSON logger; LOG_LEVEL debug|info|warn|warning|error.
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
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
