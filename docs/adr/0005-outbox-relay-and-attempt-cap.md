# ADR 0005: the outbox relay, its attempt cap and the CloudEvents encoder

## Status

Accepted (2026-10-23).

## Context

Every state change appends its events to a transactional outbox (ADR 0002, Transactions). Something has to drain the rows to Kafka, and the harness (`TestOutboxRelaysPassAttemptCap`) forbids a relay that one poison row can block forever.

## Decision

1. **Relay.** `internal/adapters/outbound/outbox` holds `NewOutboxRelay(store, sink, logger, opts...)`. A pass claims up to a batch of unpublished rows in id order, sends them one at a time and marks a row published only after the send returned nil. `Run` repeats until its context is cancelled: a full batch is followed at once by another pass, anything else sleeps `OUTBOX_RELAY_INTERVAL` (default 1 s). The relay never dials at construction: a broker that is down at boot only makes passes fail.
2. **Store.** `postgres.Outbox.Drain` claims with `FOR UPDATE SKIP LOCKED` (two replicas never claim the same row), stops at the first send failure (a later event of an aggregate never overtakes an earlier one that is being retried), records `attempts` and `last_error` on the failed row and commits the rows already sent. `Append` encodes before it writes, so an event the encoder refuses rolls the whole unit of work back.
3. **At-least-once.** A crash between the send and the `UPDATE` republishes that row with the SAME CloudEvents `id` (minted at append and stored in `outbox_events.event_id`); consumers deduplicate on it.
4. **Attempt cap.** `WithMaxAttempts(n)` (env `OUTBOX_MAX_ATTEMPTS`, default 20, `0` = unlimited, a negative or unparsable value falls back to 20 with a warning) excludes a row from future claims once it failed `n` times. The row stays in `outbox_events`; the failure that reaches the cap is logged once at ERROR with id, topic, type and key; rows behind it, including rows with the same key, overtake it. An operator re-arms it with `UPDATE outbox_events SET attempts = 0 WHERE id = ...`. **Trade-off:** after the cap a later event of the same promise can reach the topic before the capped one, so consumers must not rely on per-key order across a capped row.
5. **Sink.** `kafka.RelaySink` writes each message to the topic it names with one writer that has no fixed topic, `RequireAll` acks, the `Hash` balancer on the key and `AllowAutoTopicCreation`. The first write to a new topic can fail with `UnknownTopicOrPartition`; that is an ordinary send failure, the row keeps its place and the next pass publishes it. `/readyz` therefore never asks for topics (ADR 0006).
6. **Encoder.** `kafka.Encoder` builds every envelope through `internal/adapters/kafka/cloudevents` (ADR 0004). Integration events go ONLY to `retailnetwork.delivery-promise.events`, analytics-only events ONLY to `retailnetwork.delivery-promise.analytics`. The payloads are pinned by golden exact-JSON tests (`internal/adapters/outbound/kafka/testdata`, one file per event and per variant, e.g. `PromiseBroken_late`, `PromiseBroken_lost`, `PromiseMade_nominal`).

## Consequences

- A write is accepted while Kafka is down; the rows wait in `outbox_events` and leave in id order when the broker is back.
- `PromiseBroken.cause` (`LATE`, `LOST`, `RETURNED`) is part of the golden files; a change of the payload fails them.
