---
name: how-to-add-a-rest-endpoint
description: Add or change a REST endpoint in this service in the hexagonal order (domain invariant, use case, handler, apis/openapi.yaml, godog scenario). Use when touching internal/adapters/inbound/http, apis/openapi.yaml, or exposing a use case over HTTP.
---

# How to add a REST endpoint

Follow this order: domain first, adapter last. A handler written before the
invariant it relies on validates nothing and gets bypassed.

The worked example is `POST /promises/{promiseId}:confirm`:
`internal/domain/promise/promise.go` `Confirm` + `internal/domain/book/book.go`
-> `internal/application/usecases/lifecycle.go` `ConfirmPromise` ->
`internal/adapters/inbound/http/handlers.go` `handleConfirm`. The read side example
is `GET /promises/{promiseId}` (`lifecycle.go` `GetPromise` -> `handleGetPromise`).

## 1. Domain first

Does the rule already exist in `internal/domain/<offer|promise|book|policy|search>/`?
A handler holds no business logic. A new rule goes into the aggregate with a
table-driven test in the same package (`promise_test.go`, `book_test.go`, ...) and a
named `Err...` variable. Pure Go: no I/O, `now` and ids passed in. Then re-run the
mutation gate for that package (`gremlins unleash ./internal/domain/<pkg>`).

## 2. Application: the use case

Add a method to `Service` in `internal/application/usecases/` (`lifecycle.go` for
promise commands and queries, `quote.go`, `make_promise.go`, `risk.go`, `judge.go`).
A command runs through `s.tx`: load, domain call, `Save` and `Outbox.Append` in ONE
`ports.UnitOfWork`, retried on `ports.ErrConcurrentUpdate`. Return a view from
`internal/application/views`. If a new repository method is needed, add it to
`internal/application/ports/ports.go` (interfaces only), implement it in
`internal/adapters/outbound/postgres/` AND `internal/adapters/outbound/memory/`, and
extend the shared suite in `internal/adapters/outbound/repocontract/` so both run it.
Unit-test the use case in `internal/application/usecases/*_test.go`.

## 3. Adapter: the handler

In `internal/adapters/inbound/http/`:

- add the request body to `dto.go` (strict decoding, unknown fields are 400);
- add the method to the `API` interface and the route to `NewRouter` in `server.go`,
  the handler to `handlers.go`;
- register a state-changing route with `r.Method(http.MethodPost, pattern,
  s.idempotent(handler))` so it sits behind the Idempotency-Key middleware; a read
  uses `r.Get`;
- map a new domain `Err` in `errors.go` `problemCatalogue` (kebab-case slug, the
  status the OpenAPI documents) and add it to `handlers_test.go`
  `TestEveryDomainErrorIsItsDocumentedProblem`;
- `*usecases.Service` already satisfies `API`; `cmd/api/main.go` `run` passes it, so
  the build fails until the method exists.

## 4. Contract: apis/openapi.yaml

Describe the operation, its `Idempotency-Key` parameter (writes), the success body and
every problem it can return, then lint:
`npx --yes @stoplight/spectral-cli lint apis/openapi.yaml --ruleset .spectral.yaml`
(the `api-lint` CI job runs the same). Keep `.claude/rules/rest-api.md` and the
README in step.

## 5. Behaviour

Add the happy path and each refusal to `features/*.feature` (`features/steps_*_test.go`
drive the use cases; refusals are named by the model's `Err` name) and a case to
`internal/adapters/inbound/http/handlers_test.go`. The real middleware and Postgres are
proven by `idempotency_integration_test.go` and `cmd/api/e2e_integration_test.go`.

## 6. Verify before opening the PR

`make check`, `make coverage`, `make bdd`,
`go test -tags=integration ./internal/adapters/inbound/http/ ./cmd/... -race -count=1`,
and the spectral lint above.
