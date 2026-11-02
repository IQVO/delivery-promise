# ADR 0012: adopt the fleet REST API guideline

## Status

Accepted (2026-11-01).

## Context

The warehouse-systems fleet publishes one REST API standard (noun URLs, one idempotency contract, one list
envelope). This service's REST surface (ADR 0006) diverged from it in three ways: command routes with a `:verb`
suffix, an idempotency key scoped per route whose reuse answered `422`, and two collection reads
(`GET /promises?orderId=`, `GET /promise-books?nodeId=&date=`) returning an unpaged `{items}` object. The change is a
hard cutover shipped as one set with the other services: no alias, no dual route, no toggle. Consumers are updated in
their own pull requests.

## Decision

- **Noun routes.** `POST /delivery-offers:quote` becomes `POST /delivery-quotes`; `POST /promises/{id}:confirm` becomes
  `POST /promises/{id}/confirmations`; `POST /promises/{id}:cancel` becomes `POST /promises/{id}/cancellations`. The
  request and response bodies are unchanged. The AsyncAPI description of the confirm trigger follows.
- **Idempotency-Key is required on every state-changing POST except the quote (optional there), and global.** The
  lookup key is the `Idempotency-Key` alone (migration `0003`, additive: existing rows stay valid). The same key with
  the same method, path and body replays the stored answer byte for byte with `Idempotency-Replayed: true`; the same
  key with a different method, path or body is `409 idempotency-key-reused` (was `422`). A key that is not 1-128
  printable ASCII characters is `400 idempotency-key-invalid`; a missing one `400 idempotency-key-required`. The key
  row commits in the same Postgres transaction as the aggregates and the outbox rows (ADR 0006, savepoint).
- **Keyset pagination on both collections.** `limit` (1-500, default 100; empty or repeated is `400 invalid-limit`)
  and `cursor` (opaque base64url JSON of the last row's sort key and id; undecodable or repeated is `400
  invalid-cursor`). The answer is `{items, nextCursor}`, `nextCursor` absent on the last page. Any query parameter the
  operation does not declare is `400 unknown-query-parameter`. Sort keys: `(groupNo, promiseId)` for promises,
  `(cutoffAt, bookId)` for promise books. The reference helper is copied verbatim to
  `internal/adapters/inbound/http/page`.
- **Both collections stay bounded by a mandatory parent filter** (one order, one node-day), so the use cases still
  read the parent's rows and the HTTP adapter applies the keyset over that bounded, ordered set; no OFFSET exists.
  The MCP and A2A read tools keep the unpaged use-case result. No `x-pagination-exempt` is declared: STANDARD 3.5
  does not exempt a list because its parent key bounds it.
- The OpenAPI contract is linted by the fleet Spectral ruleset (`.spectral.yaml`, R1-R8) in the `api-lint` job.

## Consequences

- Breaking for every caller of the renamed routes, the retired `422` on key reuse and the list readers; the
  consumers are listed in the pull request and change in lockstep.
- A very large order or node-day would still be read in full by the use case before slicing; push the keyset into the
  repository queries if those parents ever grow beyond a few dozen rows.
- The quote poll writes an `idempotency_keys` row only when the caller sends a key.
