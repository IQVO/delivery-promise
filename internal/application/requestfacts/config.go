// Package requestfacts is the best-effort path of the request-level promise
// coverage fact (ADR 0032): the request handler hands a small value to a
// bounded in-memory Queue after the response is written, and a single
// background worker stores one analytics-only outbox row per fact in its own
// short transaction. The request path never waits on, fails on or is slowed by
// the fact, and a lost fact changes no response.
package requestfacts

import "time"

// Defaults of the optional settings (env DELIVERY_REQUEST_FACTS_QUEUE_SIZE and
// DELIVERY_REQUEST_FACTS_FLUSH_INTERVAL); the feature itself is OFF unless
// DELIVERY_REQUEST_FACTS_ENABLED is true.
const (
	// DefaultCapacity is how many facts wait for the worker: about 1000
	// requests of burst per flush interval, a few hundred KiB of memory.
	DefaultCapacity = 1000
	// MaxCapacity bounds the capacity a setting may ask for.
	MaxCapacity = 100000
	// DefaultFlushInterval is how often the worker wakes to drain the queue.
	DefaultFlushInterval = time.Second
	// WriteTimeout bounds one fact's transaction; a slower one is dropped.
	WriteTimeout = time.Duration(2e9) // 2s
	// ShutdownDrain bounds the final drain after the worker is stopped.
	ShutdownDrain = time.Duration(5e9) // 5s
	// warnEvery rate-limits the WARN logs of drops: one per interval, with the
	// number of drops since the last one.
	warnEvery = time.Duration(10e9) // 10s
)

// Why a fact was lost; the value of the reason attribute of the dropped
// counter.
const (
	ReasonQueueFull   = "queue_full"
	ReasonWriteFailed = "write_failed"
	ReasonInternal    = "internal_error"
	ReasonShutdown    = "shutdown"
)

// Config sizes the Queue. Zero or out-of-range values take the defaults.
type Config struct {
	// Capacity is the bound of the queue; a full queue drops the NEWEST fact.
	Capacity int
	// FlushInterval is how often the worker drains what is queued.
	FlushInterval time.Duration
}

// normalised replaces an unset or out-of-range value by its default.
func (c Config) normalised() Config {
	if c.Capacity < 1 || c.Capacity > MaxCapacity {
		c.Capacity = DefaultCapacity
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = DefaultFlushInterval
	}
	return c
}
