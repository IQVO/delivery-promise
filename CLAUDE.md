# delivery-promise

harness-template: v4

Retail network, bounded context Delivery Promise (Core, commerce tier). Quotes delivery offers only from a feasible witness path (node cutoff, scheduled departures, delivery wave with room), holds capacity in a PromiseBook until the order is placed or the hold expires, and judges every promise kept or broken. Publishes twelve event types (six integration, six analytics-only), consumes 34 types from eight producers and calls no sibling.
Design source of truth: IQVO/warehouse-docs `network/model/model.yaml` (context delivery-promise). This service belongs to the retail network, not the warehouse-systems fleet.
This project is a domain-driven design study, not associated with any company: never name a real company, brand or product in code, comments, ADRs, specs, tests or fixtures (README `## Disclaimer`).

## Commands
`make check` (fmt, vet, build, lint, unit + handler + BDD; the pre-push gate), `make coverage` (>= 90% over internal/domain + internal/application), `make mutation` (gremlins once per internal/domain/<pkg>, thresholds efficacy 75 / mutant-coverage 90, below the weakest measured package: search 77.78 / 95.19),
`make arch-test`, `make integration` (testcontainers Postgres + Kafka, needs Docker; build tag `integration`), `make vuln`. Lint: `golangci-lint run --allow-parallel-runners ./...`.
Guides: `python3 scripts/harness/repo_lint.py`, `python3 scripts/harness/guide_lint.py` must stay clean. Never gate a commit on a piped command; run the integration suite as a background job writing to a file.

## Layout (hexagonal)
`internal/domain/{shared,calendar,policy,offer,book,promise,search}` pure Go (clock and ids passed in); `internal/application/{ports (interfaces only),readmodel,repository,views,usecases}`;
`internal/adapters/inbound/{http (chi),kafka (one consumer, eight topics)}`; `internal/adapters/outbound/{postgres,kafka,outbox,clock,ids,memory,repocontract}`; `internal/adapters/kafka/cloudevents` (the ONLY envelope builder);
`internal/testing/{pgtest,kafkatest}`; `internal/bootretry`; `cmd/api` (composition root, `run(ctx)`); `features/` (godog). Migrations are embedded in `internal/adapters/outbound/postgres/migrations`.

## Runtime rule
Real PostgreSQL and Kafka only. `DATABASE_URL`, `KAFKA_BROKERS` and `DELIVERY_PROMISE_CONSUMER_GROUP` are mandatory (boot error naming the variable, raised before anything opens; `MIGRATIONS_DATABASE_URL` optional direct DSN).
Workers: one consumer, the sweeper (`SWEEP_INTERVAL` 15s: ExpireOffers, ReleaseExpiredHolds, CloseBooks, RepriceAtRisk) and the always-on outbox relay (`OUTBOX_MAX_ATTEMPTS` 20, 0 = unlimited; `OUTBOX_RELAY_INTERVAL` 1s); the process exits non-zero when any ends on its own.
`/healthz` = liveness; `/readyz` = drain gate + Postgres ping + Kafka metadata request naming no topic. Shutdown: /readyz 503, `SHUTDOWN_DRAIN_DELAY` (5s), drain HTTP, stop consumer and sweeper, stop relay LAST, close pool. cmd/** never imports outbound/memory or repocontract. No authentication on REST. See docs/docs/overview/runtime.md.

## REST
chi router for apis/openapi.yaml: POST /delivery-quotes, GET /delivery-quotes/{quoteId}, POST /promises, POST /promises/{promiseId}/confirmations, POST /promises/{promiseId}/cancellations, GET /promises/{promiseId}, GET /promises?orderId=, GET /promise-books?nodeId=&date=, POST/GET /promise-policies. `Idempotency-Key` required on EVERY POST (the quote included); the key is GLOBAL (looked up alone, fingerprint = method + path + body hash), same key + other request = 409 `idempotency-key-reused`, replay carries `Idempotency-Replayed: true`; savepoint heals the tx when a mapped conflict aborts it (ADR 0006). List GETs page with `limit` + keyset `cursor`, body `{items,nextCursor}`.
Errors are RFC 9457 problem+json, `type` = slug (ADR 0002 A22): not-found, illegal-transition, concurrent-update, validation-failed, plus the model slugs and idempotency-key-reused, dependency-unavailable.

## Events: CloudEvents 1.0 is MANDATORY
Published: type `com.retailnetwork.commerce.delivery-promise.<promise|promisebook|deliveryoffer|promisepolicy>.<Event>`; source `/retailnetwork/delivery-promise`; dataschema `urn:retailnetwork:delivery-promise:<events|analytics>:<Event>:v1`; subject and Kafka key = aggregate root id.
Integration (events topic): PromiseAtRisk, PromiseRevised, PromiseKept, PromiseBroken (cause LATE|LOST|RETURNED), PromiseUnkeepable, PromiseBookExhausted. Analytics only: PromiseMade, PromiseConfirmed, PromiseReleased, PromiseCancelled, DeliveryOfferQuoted, PromisePolicyPublished.
Use internal/adapters/kafka/cloudevents only. Transactional outbox: aggregates + outbox rows in ONE Postgres transaction (MakePromise changes offer, promise and book in one: documented exception, G22). No `warehouse.*` topic, ever.
Consumed: dispatch on the FULL type (`Handlers` in inbound/kafka/dispatch.go), tolerant decoding, inbox `processed_events`, DLQ `<topic>.dlq`; re-read the producer's AsyncAPI (`git -C ~/warehouse-systems/<producer> show origin/feature/service:apis/asyncapi.yaml`) before changing a consumed shape (ADR 0003).
A new event: domain struct + apis/asyncapi.yaml + ADR 0004 `type:` line in the SAME commit + encoder payload + golden test (.claude/skills/how-to-add-an-integration-event).

## Domain
Aggregates, invariants and assumptions A1-A30 in docs/adr/0002; consumed contracts and local read models in 0003; outbox/relay/cap in 0005; HTTP, consumer, sweeper, readiness in 0006. A group is judged on its LAST delivered package or the first LOST/RETURNED one (A18). The node never changes after lock (A17).

## Workflow
GitFlow: feature/* -> PR into develop, CI green before merge, never push develop or main. lefthook: pre-commit fmt/vet/lint, pre-push `make check`. Commit and push after each step.
Deployment: `charts/delivery-promise` (api only; Helm chart tests `bash charts/delivery-promise/tests/render_test.sh` and `python3 charts/delivery-promise/tests/test_service_selectors.py`; CI `helm-lint` + `chart-tests`). The chart refuses to render without `database.*`, `kafka.brokers`, `config.consumerGroup`, with a `*_MODE` in extraEnv or with `terminationGracePeriodSeconds` <= drain delay + 40s. Open: registry secrets (docs/docs/operations/before-first-release.md row 11).

A2A: `cmd/a2a` (port 8093) is this context's read-only Agent2Agent agent, skills `promise_status` (read), `book_capacity` (read), decided in `docs/adr/0010-a2a-inbound-adapter.md`; delegation only (facts stay CloudEvents on Kafka), unauthenticated like REST and MCP; rules in `.claude/rules/fleet/a2a.md`.
 Agent authority: every MCP tool and A2A skill declares a level (`com.warehouse/authority` _meta / `authority:L<n>` skill tag; L1 read, L2 recommend, L3 write - ADR 0013); the MCP Streamable HTTP handler is stateless and answers the 2026-07-28 revision.

<!-- harness:scoped-rules:start (generated by tools/migrate_v3.py in warehouse-harness-template; do not hand-edit) -->
## Scoped rules and harness

Claude Code loads each rule below automatically when you touch the matching paths. OpenCode and Codex do NOT: read the rule BEFORE editing matching files.

| When touching | Read |
|---|---|
| `cmd/dp-projector/**`, `cmd/dp-reports/**`, `analytics/**` ... | `.claude/rules/analytics.md` |
| `internal/adapters/**/kafka/**`, `internal/adapters/outbound/outbox/**`, `apis/asyncapi*` | `.claude/rules/integration-events.md` |
| `internal/adapters/inbound/http/**`, `apis/openapi*.yaml`, `apis/openapi/**` | `.claude/rules/rest-api.md` |

Hooks (`scripts/harness/hook.py`, wired for Claude Code, Codex and OpenCode) block pushes to develop/main, `--no-verify`, bare `rm -rf`, and edits to generated files, and feed gofmt/vet findings back after each edit. Before saying "done" run `make check-fast`; the full gate is `make check-all`. `HARNESS_OFF=1` disables the hooks when debugging the harness itself.
<!-- harness:scoped-rules:end -->
