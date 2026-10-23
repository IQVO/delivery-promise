---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Route -> use case, kept in sync with `apis/openapi.yaml`. Routes are registered in
`internal/adapters/inbound/http/server.go` `NewRouter`, handlers are in `handlers.go`,
the wiring is `cmd/api/main.go` `run`:

- `POST /delivery-offers:quote` -> `QuoteDeliveryOffers` (200; `Idempotency-Key` optional)
- `GET /delivery-offers/{offerId}` -> `GetOffer`
- `POST /promises` -> `MakePromise` (201; a replay of the same offer answers 200)
- `GET /promises/{promiseId}` -> `GetPromise`
- `GET /promises?orderId=` -> `ListPromisesByOrder` (`{"items":[...]}`)
- `POST /promises/{promiseId}:confirm` -> `ConfirmPromise` (body `{orderId, groupNo}`; repeating it is a no-op)
- `GET /promise-books?nodeId=&date=` -> `ListPromiseBooks`
- `POST /promise-policies` -> `PublishPromisePolicy` (201)
- `GET /promise-policies/current` -> `CurrentPolicy`
- `GET /healthz` (liveness), `GET /readyz` (readiness): not business operations.

## Conventions

- **Errors** are RFC 9457 `application/problem+json`: `type` is
  `https://retailnetwork.example/problems/<slug>` (`errors.go` `problemCatalogue`),
  plus `title`, `status`, `detail`, `instance`. Shared: `not-found` 404,
  `illegal-transition` 409, `concurrent-update` 409, `validation-failed` 422; one
  slug per model `Err...` (`promise-capacity-exhausted`, `book-closed`,
  `offer-not-quotable`, `promise-hold-expired`, `promise-closed`,
  `revision-moves-earlier`, `group-spans-nodes`, `invalid-promise-terms`,
  `nominal-basis-not-allowed-for-tier`); an unmapped error is an opaque 500
  `internal-error`. A new `Err` needs a catalogue row and a row in
  `handlers_test.go` `TestEveryDomainErrorIsItsDocumentedProblem`.
- **Idempotency-Key** is required on `POST /promises`, `POST /promises/{id}:confirm`
  and `POST /promise-policies` (`idempotency.go`), optional on the quote. The key is
  stored per (route, key); same key + same body replays the first response with
  `Idempotency-Replayed: true`, same key + other body is 422
  `idempotency-key-reused`, a missing key 400 `idempotency-key-required`; keys live
  24 h in `idempotency_keys`. The handler runs in a SAVEPOINT: a 4xx is stored but
  leaves no writes, a 5xx is never stored. A new state-changing route registers
  through `Server.idempotent`, never a bare `r.Post`.
- **NUL** (U+0000) is rejected before anything else (`nul.go`).
- Bodies are decoded strictly (`decodeJSON`): unknown fields are 400
  `malformed-request`; a value the domain refuses is 422.
- **Handlers hold no business logic**: decode, call a use case, encode the view.
- **Middleware** order (`cmd/api/main.go`): access log outermost, then `Recoverer`
  (a panic becomes a 500 problem and is logged as such), then the router.
- **Auth**: none. REST is unauthenticated, as in the rest of the network
  (`internal/architecture/fitness_test.go` `TestNoAuthMiddlewareReintroduced` keeps it so).
- Unit tests (`handlers_test.go`) drive the router over a fake `API`; the real
  middleware is proven on Postgres in `idempotency_integration_test.go`, the whole
  service in `cmd/api/e2e_integration_test.go`.
