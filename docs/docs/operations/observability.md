---
id: observability
title: Observability
sidebar_label: Observability
description: "Logs, metrics, traces, exporter settings and the dashboard and alerts of Delivery Promise, as they exist in the code and in warehouse-infra."
---

# Observability

The service follows the fleet observability standard ([ADR 0009](/docs/adr/0009-fleet-observability-standard)): JSON logs on stdout, OTLP/gRPC metrics and traces to a Collector, and `trace_id`/`span_id` on every log record written inside a span. All three binaries (`cmd/api`, `cmd/mcp`, `cmd/a2a`) call `telemetry.Setup` before their router is built (`internal/adapters/outbound/telemetry/telemetry.go`).

## Exporter configuration

| Variable | Default | Effect |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP/gRPC target for traces **and** metrics. `host:port` or a URL. |
| `OTEL_SERVICE_NAME` | `delivery-promise` (api), `delivery-promise-mcp`, `delivery-promise-a2a` | `service.name` resource attribute |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |
| `LOG_LEVEL` | `info` | log threshold, see below |

Export is deliberately non-blocking: the gRPC exporters connect lazily, so an absent Collector drops telemetry and never delays boot or requests (SDK errors are logged at DEBUG: `opentelemetry error`). Metrics are pushed every 15 s; the final flush at shutdown is bounded to 5 s. W3C `traceparent` and baggage propagators are installed globally. The chart's `otel.*` values render these variables, `otel.enabled=false` renders none. Full table: [Configuration](/docs/operations/configuration).

## Metrics

Source: `internal/adapters/outbound/telemetry/promise_metrics.go`, `internal/adapters/inbound/http/server.go`, `cmd/mcp/router.go`, `cmd/a2a/router.go`.

| Instrument | Type | Unit | Attributes | Emitted by |
| --- | --- | --- | --- | --- |
| `promising.promises.made` | Int64 counter | `{promise}` | `outcome` = `made` or `refused`; `reason` (only on `refused`) = the problem slug, for example `offer-not-quotable`, `promise-capacity-exhausted`, `internal-error` | `cmd/api`, every `MakePromise` attempt. Prometheus name `promising_promises_made_total` |
| `http.server.request.duration` | Float64 histogram | `s` | `http.method`, `http.scheme`, `http.route` (chi route pattern, when matched) | all three binaries, `otelchimetric.NewServerRequestDuration`. Prometheus name `http_server_request_duration_seconds` |
| Go runtime metrics | various | various | none | all three binaries (`runtime.Start`), goroutines, GC and heap; the dashboard reads `go_goroutine_count` and `go_memory_used_bytes` |

Reading `promising.promises.made`: a falling `made` share with a rising `offer-not-quotable` or `promise-capacity-exhausted` means shoppers are offered dates the network can no longer keep (expired offers, full nodes); a rising `internal-error` means the service itself is failing. There is **no** instrument for outbox depth, consumer lag, sweep counts or DLQ volume; use the SQL and logs in the [Runbook](/docs/operations/runbook).

## Traces

| Span | Kind | Created by | Attributes |
| --- | --- | --- | --- |
| otelchi server span, named after the chi route pattern | server | the chi router of all three binaries | standard HTTP attributes; named after the route pattern, never the raw path |
| `<topic> process` | consumer | `internal/adapters/inbound/kafka/tracing.go`, one per handling attempt | `messaging.system=kafka`, `messaging.destination.name`, `messaging.operation.name=process`; errors are recorded on the span |
| `a2a.skill <skill>` | internal | `internal/adapters/inbound/a2a/skills.go` | `a2a.skill`; status Error with the problem detail when the skill answers a problem |

Trace continuity across Kafka: the outbox stores the W3C `traceparent`/`tracestate` of the writing request with each row (`internal/adapters/outbound/postgres/outbox_trace.go`), the relay publishes them as message headers, and the consumer extracts them so its span is a child of the producer's. A message without headers starts a new trace. The MCP adapter creates no tool-level span of its own; MCP calls appear as the HTTP server span of the request.

## Logs

JSON via `slog` on stdout (`newLogger`, `LOG_LEVEL` = `debug`, `info`, `warn`/`warning`, `error`). Loki label `app` is `delivery-promise` for the api pod. Fields:

| Field | Where | Meaning |
| --- | --- | --- |
| `time`, `level`, `msg` | every record | slog defaults |
| `trace_id`, `span_id` | records written with a span-carrying context (`*Context` slog calls) | correlation with Tempo/Jaeger. Records from startup, shutdown and background loops carry none |
| `request_id` | access log | the chi request id |
| `method`, `route`, `status`, `duration_ms`, `bytes` | access log line `http request` | route is the matched pattern; 5xx is logged at ERROR, 4xx at WARN, otherwise INFO. `/healthz` and `/readyz` are not logged |
| `worker`, `error` | `worker stopped` (ERROR) | a background worker returned on its own; the process then exits non-zero |
| `pass`, `count` | `sweep pass` (INFO) / `sweep pass failed` (ERROR) | one sweeper pass |
| `outbox_id`, `topic`, `event_type` | `outbox relay: row reached the attempt cap...` (ERROR) | a parked outbox row |
| `variable`, `value`, `default` | `invalid duration; using the default` (WARN) | a bad optional duration variable |
| `service_name`, `otlp_endpoint` | `telemetry configured` (INFO, boot) | exporter in effect |
| `topics`, `group_id`, `brokers` | `consumer running` (INFO, boot) | consumer wiring |

Useful boot lines, in order for `cmd/api`: `telemetry configured`, `postgres adapters configured`, `http server listening`, `consumer running`, `sweeper running`, `outbox relay running`. Shutdown logs `shutdown: readiness flipped to not-ready; waiting for traffic to drain`.

## Dashboards and alerts

Only what exists in `warehouse-infra` on `develop`:

- **Dashboard** `warehouse-delivery-promise` ("Warehouse - Delivery Promise", `terraform/dashboards/contexts/delivery-promise.json`): Kong request rate, 5xx error rate and p95 latency of the context's routes; service HTTP RED (request rate by route, 5xx by Kong route, request duration p95 by process, from `http_server_request_duration_seconds`); the business panel `promising_promises_made_total` by `outcome`; Go goroutines and memory; Loki log stream, log volume by level and errors/warnings for `{app="delivery-promise"}`.
- **Alerts** (`terraform/alerting/warehouse-alert-rules.json`, groups `warehouse-delivery-promise` and `warehouse-delivery-promise-slo`): more than 5 % of requests on a Kong route answering 5xx; a container restarting (two or more restarts in 15 minutes, `pod=~"delivery-promise-.+"`); API availability SLO fast burn and slow burn; API latency SLO (400 ms upstream) fast burn and slow burn.
- **Not present:** alerts or panels for outbox backlog, parked outbox rows, consumer lag or dead letters. Because the process exits on any worker failure, a wedged relay or consumer shows up as the restart alert.

The alert annotations' `runbook_url` still points at the pre-move path `docs/operations/runbook.md` on `develop`; the runbook now lives at [Runbook](/docs/operations/runbook) on this site.
