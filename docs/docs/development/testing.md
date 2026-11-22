---
id: testing
title: Testing
sidebar_label: Testing
description: "The test pyramid of Delivery Promise: unit, integration, godog features, contract, mutation and fitness tests, the make targets and the CI jobs."
---

# Testing

The pyramid as it exists in the repository. Everything below runs without a hand-started database or broker: integration tests boot their own Postgres and Kafka with testcontainers (the fitness tests `TestPostgresIntegrationTestsUseTestcontainers` and `TestKafkaIntegrationTestsUseTestcontainers` fail a test that reads `DATABASE_URL` or `KAFKA_BROKERS` and skips).

| Layer | Where | Run with | Needs |
| --- | --- | --- | --- |
| Unit and handler tests | `internal/domain/*`, `internal/application/usecases`, `internal/adapters/**`, `cmd/*` (`*_test.go`) | `make test` (`go test ./... -race`) | nothing |
| Behaviour (godog / Gherkin) | `features/` | `make bdd` (`go test ./... -run TestFeatures -v`) | nothing (in-memory test doubles, `Strict: true`) |
| Integration | files named `*_integration_test.go` under the build tag `integration` | `make integration` (`go test -tags=integration ./... -race -count=1`) | Docker only |
| Contract | event catalogue, MCP tool registry, OpenAPI/AsyncAPI lint | `make arch-test` for the first two; `spectral` in CI for the specs | nothing / Node |
| Mutation | `internal/domain/<pkg>` with gremlins | `make mutation`, `make mutation-full` | `gremlins` v0.6.0 |
| Architecture fitness | `internal/architecture` | `make arch-test` | nothing |

## Unit and handler tests

About 80 test files. The domain packages (`book`, `calendar`, `offer`, `policy`, `promise`, `search`, `shared`) are pure and tested without doubles. Use cases are tested over the in-memory adapters in `internal/adapters/outbound/memory` (test doubles that `cmd/**` must never import, enforced by `TestRuntimeBinaryHasNoInMemoryAdapters`). The HTTP adapter has `httptest` handler tests, idempotency and readiness tests; the consumer has loop tests with a fake reader (retry, dead-letter, commit-after-handle); the outbox relay has its own tests. `make coverage` runs the whole suite with `-race` and **gates 90 %** over `internal/domain/...` and `internal/application/...` (the only place the scope is defined; `repo_lint` R11 rejects a workflow that redefines it).

## Godog features

Ten files, **135 scenario definitions** (`git grep -c -E "^[[:space:]]*Scenario" -- 'features/*.feature'`, which counts `Scenario` and `Scenario Outline`); the runner executes them as **161 scenarios** because the Examples rows of the Outlines expand (the `161 scenarios (161 passed)` line of `go test ./features -run TestFeatures -count=1 -v`).

| File | Definitions | Covers |
| --- | --- | --- |
| `quote_rules.feature` | 29 | QuoteDeliveryOffers rules: scorecard handicap, offer expiry and policy knobs, packages per order, constraint naming, delivery wave rule, SAME_DAY, shipment grouping, tier order deadline |
| `lifecycle_rules.feature` | 25 | promise lifecycle: hold expiry sweep and confirm, cancel, flag at risk and re-promise, predicted late (ADR 0021), judgement, shared tracking numbers (ADR 0025), projection guards |
| `capacity_rules.feature` | 18 | PromiseBook: book share, hold, exhaustion, release, close at cutoff, and the availability a quote sells (ADR 0015) |
| `promises.feature` | 13 | make, confirm, hold expiry, capacity, revisions |
| `rest.feature` | 11 | the REST contract through the real router |
| `quoting.feature` | 9 | the witness-path search, tiers, constraints |
| `keeping.feature` | 9 | judging kept or broken, re-promising on risk |
| `calibration.feature` | 5 | promise calibration (ADR 0020) |
| `consolidated.feature` | 5 | the opt-in consolidated tier (ADR 0024) |
| `stale_facts_rules.feature` | 11 | capacity facts, node status and listing suppression that arrive out of order never overwrite a newer one; a retired node is terminal (ADR 0028, ADR 0029) |

The three `*_rules.feature` files pin rules the context's documentation states (ADR 0002, 0003, 0015, 0021, 0022, 0025). A scenario that describes a documented rule the code does not yet honour is tagged `@known-bug`: it is excluded by default (`BDD_TAGS` defaults to `~@known-bug`) and runs alone with `BDD_TAGS=@known-bug`, where it must fail. No scenario carries the tag today.

The runner is `features/features_test.go` (`TestFeatures`, `Paths: "."`, `Strict: true`, so an undefined step fails); steps live in `features/steps_*_test.go`. No Go file may sit at the repository root (`TestNoGoFilesAtRepoRoot`).

## Integration tests

Ten files tagged `integration`: the Postgres adapter (`main_integration_test.go`, `repositories_integration_test.go`, `outbox_integration_test.go`, `outbox_trace_integration_test.go`, `window_time_zone_integration_test.go`), the idempotency middleware (`internal/adapters/inbound/http/idempotency_integration_test.go`, the replay-class template `idempotency_replay_classes_integration_test.go` and `idempotency_replay_extra_integration_test.go`, and `cmd/api/idempotency_replay_integration_test.go`: a refused `concurrent-update` and a full book through the real router release their key, ADR 0031) and the three binaries (`cmd/api/main_integration_test.go`, `cmd/api/e2e_integration_test.go`, `cmd/mcp/main_integration_test.go`, `cmd/a2a/main_integration_test.go`). `cmd/api/e2e_integration_test.go` boots the real entry point on testcontainers Postgres and Kafka, proves the outbox publishes, and stops Kafka to prove `/readyz` goes 503 naming kafka while `/healthz` stays 200 and a write is still accepted.

## Contract tests

- **Events:** `TestEventCatalogueMatchesContract` (`internal/architecture/catalogue_fitness_test.go`) ties the event catalogue in code to `apis/asyncapi.yaml`; `TestCloudEventsOnly` forbids any non-CloudEvents envelope.
- **MCP:** `TestToolSurface`, `TestWriteVerbSensorFailsOnWriteNames` and `TestToolRegistryGolden` (`internal/adapters/inbound/mcp/governance_test.go`) pin the six tools, forbid write verbs and pin the registry (`TestToolRegistryGolden`).
- **REST and events specs:** `spectral lint` of `apis/openapi.yaml` and `apis/asyncapi.yaml` (CI job `api-lint`, `--fail-severity=warn`). There is no test that replays the OpenAPI document against the live router beyond `features/rest.feature`.

## Mutation tests

`.gremlins.yaml` is read by every gremlins run: one worker, `timeout-coefficient: 30`, thresholds `efficacy: 87` and `mutant-coverage: 94`. `make mutation` loops over **every** package under `internal/domain/` (shared, calendar, policy, offer, book, promise, search) and stops on the first failure; the CI job `mutation-fast` does the same on every push and PR; `make mutation-full` (job `mutation`, weekly and manual) runs the whole `./internal/domain` tree. The measured efficacy per package is recorded in the file's header (weakest: `search`, 87.88-88.89 %).

## Fitness tests

`make arch-test` runs `internal/architecture` (31 test functions). The layer rule is `TestHexagonalArchitecture`; the others each guard one fleet rule:

| Test | Guards |
| --- | --- |
| `TestMCPAdapterDependencyRule`, `TestA2AAdapterDependencyRule`, `TestA2AAdapterIsReadOnly`, `TestNoOutboundA2AClient` | the MCP and A2A adapters are read-only and call no sibling |
| `TestNoAuthMiddlewareReintroduced` | no authentication middleware on any surface |
| `TestRuntimeBinaryHasNoInMemoryAdapters` (+ `SensorBites`) | `cmd/**` never links the memory adapters |
| `TestNoGoFilesAtRepoRoot` (+ variants) | no Go file at the root |
| `TestOutboxRelaysPassAttemptCap` | every relay is built `WithMaxAttempts` |
| `TestKafkaWritersSetRequiredAcks`, `TestKafkaWriterTopicNotSetTwice`, `TestDeadLetterCallsGuardCancellation` | writer settings and dead-letter shutdown behaviour |
| `TestKafkaConsumerGroupNeverHardcodedInline`, `TestReplayConsumersSetCommitInterval` | stable group ids from the environment; no async commit |
| `TestPostgresIntegrationTestsUseTestcontainers`, `TestKafkaIntegrationTestsUseTestcontainers` | integration tests own their infrastructure |
| `TestNoEventEnvelopeToggleOrFlatEnvelope`, `TestCloudEventsOnly`, `TestEventCatalogueMatchesContract` | CloudEvents 1.0 structured mode only; catalogue equals contract |
| `TestMCPOptionalToolInputsCarryOmitempty` | optional MCP inputs are optional in the schema |

## Make targets

| Target | Effect |
| --- | --- |
| `make check` | `fmt-check vet build lint test`, the pre-push gate (lefthook runs it on push) |
| `make check-all` | `check` plus `coverage arch-test bdd` |
| `make check-fast` | `fmt-check vet arch-test` plus tests of the packages changed against HEAD (agent Stop hook) |
| `make coverage` | the 90 % gate |
| `make integration` | testcontainers suite, not part of `check` |
| `make mutation` / `make mutation-full` | gremlins |
| `make vuln` | `govulncheck ./...` |
| `make guide-lint`, `make harness-test` | agent-guide and hook checks |

Helm chart tests are outside `make`: `bash charts/delivery-promise/tests/render_test.sh` and `python3 charts/delivery-promise/tests/test_service_selectors.py`. The web remote has its own suite (`cd web && npm run lint && npx tsc -b && npm test && npm run build`).

## CI jobs (`.github/workflows/ci.yml`)

Triggers: push and pull request on `main` and `develop`, a weekly Monday 06:00 UTC schedule and manual dispatch.

| Job | Runs | Blocking |
| --- | --- | --- |
| `lint` | golangci-lint v2.14.0 | yes |
| `guide-lint` | `scripts/harness/guide_lint.py`, `test_hook.py` | advisory (`continue-on-error`) |
| `complexity` | gocyclo, gocognit, cyclop, funlen, nestif with the thresholds of `.golangci.yml` | yes |
| `test` | `go build`, `go vet`, `make coverage` | yes |
| `bdd` | `go test ./... -run TestFeatures -v` | yes |
| `integration` | `go test -tags=integration ./... -race -count=1` | yes |
| `mutation-fast` | gremlins on each `internal/domain` package | yes |
| `mutation` | exhaustive gremlins (schedule and dispatch only) | opens a `harness:red` issue on failure |
| `drift` | dead code, `go mod tidy -diff`, knip, coverage-quality (schedule and dispatch only) | advisory |
| `api-lint` | spectral on OpenAPI and AsyncAPI | yes |
| `vuln` | `govulncheck` | yes |
| `helm-lint` | `ct lint --charts charts/delivery-promise` (pull requests only) | yes |
| `chart-tests` | every `charts/delivery-promise/tests/*.sh` and `test_*.py`; a test that prints `SKIP` fails | yes |
| `arch-test` | `go test ./internal/architecture/... -v` | yes |
| `web` | lint, tests and build of `web/` | yes |
| `docker-publish` | image build and push (push to `main`; needs lint, complexity, test, bdd, integration, mutation-fast, vuln, api-lint, arch-test) | release gate |
| `release` | packages `delivery-promise-<version>.tgz` (push to `main`) | release |

The documentation site has its own workflow, `.github/workflows/docs.yml` (build on pull request, deploy on push to `main`).
