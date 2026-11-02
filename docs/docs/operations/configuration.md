---
id: configuration
title: Configuration
sidebar_label: Configuration
description: "Every environment variable read by the three Delivery Promise binaries (cmd/api, cmd/mcp, cmd/a2a): default, required or not, and what it does."
---

# Configuration

Delivery Promise ships three binaries. Each reads its own environment; there is no config file and no flag parsing. A variable that several binaries read has a row in **each** table below, with that binary's own default. Test-only variables are left out.

Missing or malformed mandatory variables are refused **before** anything is opened or bound: the boot error names the variable (`cmd/api/config.go`, `cmd/a2a/main.go`, `cmd/mcp/main.go`). The Helm chart (`charts/delivery-promise`) renders exactly these variables, see [Runbook](/docs/operations/runbook#deployment).

Durations accept a Go duration (`500ms`, `2s`) or a plain number of seconds. An unset, malformed or non-positive value logs a warning and uses the default (`parseDuration` in `cmd/api/config.go`), except where a row says otherwise.

## `cmd/api` (REST API, consumer, sweeper, outbox relay)

Source: `cmd/api/config.go`, `cmd/api/telemetry.go`, `cmd/api/workers.go`, `cmd/api/main.go`.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | Listen address of the REST server. Keep the chart's `service.targetPort` in step. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the runtime pool. Unset: `DATABASE_URL is required: this service runs on real Postgres, there is no in-memory mode`. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (non-pooled) DSN used only for the boot migrations. golang-migrate takes a session advisory lock that a transaction pooler such as PgBouncer cannot honour. |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated `host:port` list used by the relay, the consumer and `/readyz`. Blank entries are dropped; an empty list is a boot error. |
| `DELIVERY_PROMISE_CONSUMER_GROUP` | none | **yes** | Stable consumer group of the inbound consumer of the eight producer topics. Never derive it from the pod name. |
| `EVENT_PUBLISHER` | empty (means `kafka`) | no | Only `kafka` (or empty) is accepted; any other value is a boot error (`EVENT_PUBLISHER=<x> is not supported`). The log sink does not exist. |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Poll interval of the outbox relay. |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Failures before the relay excludes an outbox row. `0` means unlimited; a negative or unparsable value warns and falls back to 20, so a typo never removes the cap. |
| `SWEEP_INTERVAL` | `15s` | no | How often the clock-driven sweeper runs its four passes (batch of 100 each). |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | Wait between flipping `/readyz` to 503 and closing the listener. `0` disables the wait; a negative or unparsable value warns and uses 5s. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`), `error`, case-insensitive; anything else is `info`. |
| `OTEL_SERVICE_NAME` | `delivery-promise` | no | `service.name` of spans, metrics and the Loki `app` label. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint, `host:port` or full URL (a bare `host:port` becomes `http://host:port`). Export never blocks: an unreachable Collector drops telemetry, it does not stop the service. |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute (read by `telemetry.Setup`). |

The shutdown bounds that are **not** configurable: HTTP drain 10 s, consumer and sweeper await 10 s, relay await 10 s, telemetry flush 5 s. The chart refuses a `terminationGracePeriodSeconds` that does not exceed `SHUTDOWN_DRAIN_DELAY` + 40 s.

## `cmd/mcp` (read-only MCP server)

Source: `cmd/mcp/main.go`, `cmd/mcp/telemetry.go`. This binary never dials Kafka and never runs the relay, consumer or sweeper, so it reads no `KAFKA_BROKERS`, no consumer group and no `EVENT_PUBLISHER`.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `MCP_ADDR` | `:8090` | no | Listen address of the Streamable HTTP server (`/mcp`). |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the service database that `cmd/api` owns. Unset is a boot error. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the idempotent boot migrations (this binary can boot against a fresh database before `cmd/api` has). |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`, `error`. |
| `OTEL_SERVICE_NAME` | `delivery-promise-mcp` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint; export never blocks. |
| `SERVICE_VERSION` | `dev` | no | `service.version`; also in the boot log line. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name`; also in the boot log line. |

## `cmd/a2a` (read-only A2A agent)

Source: `cmd/a2a/main.go`, `cmd/a2a/telemetry.go`. Like `cmd/mcp`, it never touches Kafka.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `A2A_ADDR` | `:8093` | no | Listen address of the JSON-RPC server. |
| `A2A_AGENT_URL` | none | **yes** | Absolute `http(s)` URL advertised on the Agent Card, for example `http://delivery-promise-a2a.<namespace>.svc.cluster.local:8093/`. A missing value, a non-http(s) scheme or an empty host is a boot error; there is no default the binary could guess. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the service database. Unset is a boot error. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`, `error`. |
| `OTEL_SERVICE_NAME` | `delivery-promise-a2a` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint; export never blocks. |
| `SERVICE_VERSION` | `dev` | no | `service.version`, also the `version` on the Agent Card. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name`; also in the boot log line. |

## Notes

- The OpenTelemetry SDK additionally honours the standard `OTEL_RESOURCE_ATTRIBUTES` through `resource.Default()`; the service does not read it itself.
- `telemetry.Setup` rewrites `OTEL_EXPORTER_OTLP_ENDPOINT` in place when it holds a bare `host:port`, so the exporters do not log a parse error at every start (`internal/adapters/outbound/telemetry/telemetry.go`).
- There is no authentication variable on any binary: REST, MCP and A2A are unauthenticated and rely on the in-cluster boundary.
- For local values that work out of the box see [Quickstart](/docs/overview/quickstart).
