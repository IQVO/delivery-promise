# ADR 0031: every problem type has a replay class; a retryable refusal is not stored under its Idempotency-Key

## Status

Accepted (2026-11-18). Supersedes the sentence "a 4xx rolls the writes back but is replayed" of ADR 0006 point 2 and
the matching sentence of the `Idempotency-Key` text of `apis/openapi.yaml` (the Decision text of those records is
not rewritten: each carries a dated pointer here). Adopts the fleet rule decided by the owner on 2026-11-18
(`.claude/rules/fleet/idempotency-and-outbox.md`, `docs/idempotency-replay-classes.md` of warehouse-harness-template).
No route, status code, problem slug, header, event or table changes: a refusal that was replayed for 24 hours is now
answered again by running the request.

## Context

`RequireIdempotencyKey` (`internal/adapters/inbound/http/idempotency.go`) claims the key row inside the request
transaction (`claimKey`: purge rows older than 24 hours, then `INSERT ... ON CONFLICT (key) DO NOTHING`), runs the
handler in a savepoint of that transaction bound into the context (`TxBinder`) and, until now, stored EVERY answer
below 500: a 2xx with its writes, a 4xx with the writes rolled back (`settleSavepoint`: "a refused request leaves no
trace"), so the same key and body replayed the refusal for 24 hours.

That is wrong for a refusal the same request could pass a moment later:

- `409 promise-capacity-exhausted` (the book is full) stays the answer for 24 hours after a cancellation freed a
  place. customer-ordering calls `POST /promises` with the deterministic key `<orderId>:<groupNo>` (and
  `:confirm`, `:cancel`): a refused order group stayed refused, and the re-quote that checkout then sends carries a
  different `offerId` under the same key, so it answered `409 idempotency-key-reused`.
- `409 concurrent-update` (another request moved the aggregate, or another policy was published) was replayed to a
  client that did the right thing and retried.

The fleet decision: a retried request is answered from the store only when its first answer is final for that
request. Each problem type carries exactly one class, declared on its catalogue entry; the middleware reads the class
and holds no list of slugs and no status range beyond 2xx and 5xx.

## Decision

1. **The class is a field of the catalogue entry.** `problemCatalogue` (`internal/adapters/inbound/http/errors.go`)
   is a slice of `{err, problem, replay}`; `replay` is `replayFinal`, `replayOutcome` or `replayRetryable`
   (`replay_class.go`). The zero value is `replayUnset`; there is no runtime default class. A catalogue entry that
   declares none, or a 5xx entry that is not RETRYABLE, fails `TestEveryCatalogueEntryDeclaresAReplayClass`; one slug
   declared with two classes fails the same test; this ADR's table is held equal to the catalogue by
   `TestTheADRDeclaresTheClassOfEveryProblem` and the RETRYABLE list of the OpenAPI text by
   `TestTheSpecListsTheRetryableProblemTypes`.
2. **The error writer marks the capture.** `writeError` tells the middleware's capturing writer the class of the entry
   it answers with (`replayMarker`, `capture.MarkReplay`). A handler that answers without a catalogue problem leaves
   the class unset.
3. **The middleware acts on the verdict, nothing else** (`capture.verdict`):

   | answer | verdict |
   | --- | --- |
   | 2xx | stored, the handler's writes commit with the key row and the stored answer (unchanged) |
   | 5xx and a panic | the whole transaction rolls back, key row included, nothing stored (unchanged) |
   | FINAL | the savepoint rolls back, the answer is stored and replayed (unchanged) |
   | OUTCOME | the savepoint COMMITS, the answer is stored in the same transaction (new, no entry uses it today) |
   | RETRYABLE | released exactly as the 5xx branch: `tx.Rollback`, key row included, nothing stored (new) |
   | no class (anything else) | released like RETRYABLE: nothing in the repo may be stored without a class |

   The released answer is flushed to the client unchanged and never carries `Idempotency-Replayed`. A 2xx, a 5xx and
   a FINAL refusal are byte-identical to before.
4. **Releasing a RETRYABLE key means the request is forgotten.** The same body under the same key runs again; a
   different body under the same key is a NEW request, not `idempotency-key-reused`: nothing is left to compare
   against. The 24-hour purge of `claimKey` rolls back with the released transaction and runs again on the next keyed
   write; a replay can only follow a `claimKey`, so no stored answer is replayed past its 24 hours.
5. **A concurrent duplicate while the first ends RETRYABLE.** The copy blocks on the first's `INSERT ... ON CONFLICT`
   (Postgres serialises on the primary-key unique index until the first transaction resolves). The first rolls back,
   the row is gone, the blocked insert succeeds: the copy is then a FRESH request and runs the handler itself. It
   never replays a released refusal and never runs alongside the first. Copies that were waiting behind it replay the
   answer of that second run. `TestACopyWaitingOnARetryableFirstRunsTheHandlerItself`: 8 copies, 1 refusal, 1 fresh
   201, 6 replayed 201s, the handler ran twice and never at once.
6. **No OUTCOME problem type exists in this service** (the only class that needs a use case to commit and then
   refuse), by reading every use case behind an idempotent route:
   - `MakePromise` (`make_promise.go`): every refusal (`o.Consume` 79, `revalidate` 82, `nodeStock` 88, `holdBook`
     92, `promise.Make` 96) returns BEFORE the first save at 104-107, and it all runs inside one `s.tx`.
   - `ConfirmPromise` (`lifecycle.go` 25-52) and `CancelPromise` (`lifecycle.go` 140-160): the aggregate method and
     the book method that can refuse (`Confirm`, `ConfirmHold`, `Cancel`, `ReleaseConfirmed`) run before `saveAll`.
   - `PublishPromisePolicy` (`lifecycle.go` 165-180): `policy.Publish` refuses before `Policies.Save`.
   - `QuoteDeliveryOffers` (`quote.go` 79-120): its only refusal is `cmd.validate()`, before anything is written; an
     offer that cannot be built is a constraint in a 200 answer.
   Because the use case writes nothing before it refuses, a refusal "leaves no trace" is also true of the aggregates:
   nothing is lost by rolling back. The OUTCOME verdict is nevertheless implemented and proven against Postgres
   (`TestAnOutcomeRefusalCommitsItsWritesWithItsStoredAnswer`, the capture marked by hand), so the first refusal that
   does commit gets its writes kept instead of silently rolled back. Its entry needs the cited lines of its use case.
7. **MCP and A2A write nothing** (ADR 0007: a `Reads` interface with no write method, `TestToolSurface` pins the tool
   set; ADR 0010: `TestA2AAdapterIsReadOnly`): no store of their own, nothing to classify. `POST /delivery-estimates` stays a bare `r.Post` with no key (ADR 0019).
8. **Retention** is enforced as the rule asks: `claimKey` purges every row older than 24 hours on each keyed write
   (`TestExpiredKeysArePurgedOnTheNextKeyedWrite`).

### The classes

The question per entry: did the command commit state before refusing (OUTCOME)? Is the answer a function of the
request alone (FINAL)? Otherwise RETRYABLE. When unsure, RETRYABLE: the refusal rolled everything back, so running it
again costs one transaction and cannot pin a false answer for 24 hours. The status and slug do not decide the class.

| Problem type | Class | Status | Reason (code of `origin/develop` at 4c39fe3) |
| --- | --- | --- | --- |
| `malformed-request` | FINAL | 400 | the body is not valid JSON for the operation (`decodeJSON`, `response.go`): a function of the request |
| `unknown-query-parameter` | FINAL | 400 | a query parameter the operation does not declare (`pageParams`, GET only): a function of the request |
| `invalid-limit` | FINAL | 400 | the paging `limit` is invalid (`page.Limit`, GET only) |
| `invalid-cursor` | FINAL | 400 | the paging cursor does not decode (`page.After`, GET only) |
| `validation-failed` | FINAL | 422 | every source on a keyed route is the request's own fields (`quote.go` `validate` 48-72, `make_promise.go` 38-43 and 72-74, `Promise.Confirm`, the policy validators) or the lines of the stored offer they must equal (`make_promise.go` 72; an offer's lines and terms never change, only its state moves) |
| `promise-hold-expired` | FINAL | 409 | `Promise.Confirm` (`promise_transitions.go` 42-43) on a PROVISIONAL promise past `holdExpiresAt`; the instant is set once, in `promise.Make` (`promise.go` 260), and no code moves it, so no retry of that confirm can pass |
| `promise-closed` | FINAL | 409 | `Promise.closed()` (`promise_transitions.go` 10-14): KEPT, BROKEN, RELEASED and CANCELLED are terminal (`State.Terminal`, `promise.go` 69); a promise never leaves them. `CancelPromise` converts it to `illegal-transition` (`lifecycle.go` 150-152), so only a confirm answers it |
| `invalid-promise-terms` | FINAL | 422 | `validateTerms` (`promise.go` 202-219) over the terms copied from the stored offer, whose terms never change (`make_promise.go` 96-99); not reachable from the request |
| `nominal-basis-not-allowed-for-tier` | FINAL | 422 | tier and basis values only (`promise.go` 206-208, `offer/validate.go` 57), no state read; two catalogue entries, one class |
| `not-found` | RETRYABLE | 404 | the offer or promise named in the request (`Offers.Get` `make_promise.go` 59, `Promises.Get` `lifecycle.go` 26 and 141) or the book of a promise (`Books.Get` in `confirm` and `cancel`). Ids are minted by this service (`IDs.NewOfferID`, `NewPromiseID`) and today no statement deletes an offer, a promise or a book, so a 404 does not turn into a success today; but nothing guards that invariant, and `ports.ErrNotFound` is shared with read-model ports. One slug, one class: when unsure, RETRYABLE (J1) |
| `promise-capacity-exhausted` | RETRYABLE | 409 | `Book.Hold` finds no room (`book.go` 168) or `revalidate` finds the node, a departure or the wave no longer available (`make_promise.go` 132, 157, 168 and 209, all through `refusal`): capacity is given back by a cancellation or a released hold, and departures and waves are event-fed read models. The case that motivated this ADR |
| `book-closed` | RETRYABLE | 409 | `Book.Hold` on a CLOSED book or one past its hold deadline (`book.go` 164-166). The same book never reopens, but checkout answers it with a re-quote whose new `offerId` sits under the same deterministic key: stored, it would answer `idempotency-key-reused` for 24 hours (J2) |
| `offer-not-quotable` | RETRYABLE | 409 | `Offer.Consume` refuses an EXPIRED or CONSUMED offer or one past `expiresAt` (`offer.go` 185-188). The refusal itself is monotone, but customer-ordering's reaction is "choose a new one" under the same `<orderId>:<groupNo>` key: stored, the re-quote would answer `idempotency-key-reused` (J2) |
| `illegal-transition` | RETRYABLE | 409 | the aggregate is not in the state the command needs yet (`Promise.illegal`, `promise_transitions.go` 17: confirm for another order 40, cancel of a PROVISIONAL promise 240, `lifecycle.go` 150-152): confirm moves PROVISIONAL on, a concurrent request may bring the state the command needs |
| `concurrent-update` | RETRYABLE | 409 | two catalogue entries, one class (`ports.ErrConcurrentUpdate`, `policy.ErrVersionNotNext`): the optimistic lock lost the race (`save` `repositories.go` 61-85, `Policies.Save`, `policy.Publish` `policy.go` 233-236) after `s.tx` retried `MaxAttempts` (3, `service.go` 20 and 73-81): the retry reads the new version and passes |
| `group-spans-nodes` | RETRYABLE | 422 | `ChooseOwners` (`owners.go` 14-19) over `nodeStock` (`availability.go` 108-134): stock listings, holds and discontinuation are event-fed read models, so the same group can ship from one owner later (J3) |
| `revision-moves-earlier` | RETRYABLE | 409 | `Promise.Revise` (`promise_transitions.go` 129), driven by the sweeper, never by a keyed route today (J4) |

Problems the capture never holds (not catalogue entries, never stored): `idempotency-key-required` and
`idempotency-key-invalid` (400) and `idempotency-key-reused` (409) are written by the middleware itself, before or
instead of the handler; `internal-error` (500) is a 5xx; `resource-not-found` (404, a NUL in the path) is answered by
`rejectNUL` before the route and the middleware. 17 distinct slugs in 19 catalogue entries: 9 FINAL, 8 RETRYABLE,
0 OUTCOME.

### Judgement calls

- **J1 `not-found` is RETRYABLE, not FINAL.** The owner's rule allows FINAL for a 404 on the command's own target when
  the ids are minted here and nothing is ever deleted. Both hold today, and a mistyped id will answer 404 again. The
  class still follows "when unsure": it is one slug for three kinds of target and for the read-model port error, and
  no test holds the "never deleted" invariant. The cost is one extra transaction for a retried mistyped id.
- **J2 `offer-not-quotable` and `book-closed` are RETRYABLE although the refusal is monotone.** A refusal that can
  never pass for the same request would be FINAL by the letter. The deciding fact is the one known caller:
  customer-ordering keys `POST /promises` by `<orderId>:<groupNo>` and answers these two refusals with a re-quote, so
  the next request differs in `offerId`. Under a stored refusal that is `idempotency-key-reused`; under a released
  key it is a new request. `promise-hold-expired` and `promise-closed` are FINAL: the next move is a new promise on a
  new path, and no sanctioned reaction is a changed request under the same key and path.
- **J3 `group-spans-nodes` is RETRYABLE although it is a 422.** The status family does not decide; the stock it reads
  does.
- **J4 `revision-moves-earlier` is RETRYABLE** only because it must have a class: no keyed route answers it.
- **J5 `invalid-promise-terms` and `validation-failed` stay FINAL** although some sources read a stored offer: its
  lines and terms never change (only its state moves), so the answer is a function of the request.

## Consequences

- A retry under the same key after `promise-capacity-exhausted`, `concurrent-update`, `not-found`, `illegal-transition`,
  `group-spans-nodes`, `book-closed` or `offer-not-quotable` runs the request again instead of replaying a refusal;
  the success is then stored. A different body under that key is a new request. A client that sent a deliberately
  reused key after one of these refusals gets its retry executed, not the old refusal.
- Deterministic-key callers (customer-ordering `<orderId>:<groupNo>[:confirm|:cancel]`) retry through these refusals
  instead of being stuck for 24 hours. They keep a stored refusal only for FINAL types, which they cannot retry into
  success. This repository changes no caller; customer-ordering needs no change to benefit.
- A released refusal costs one rolled-back transaction per retry, and the refused request writes no key row, so the
  key table no longer grows with refusals.
- A new problem type is classified by the PR that adds it: the unit test is red otherwise, and the table above, the
  OpenAPI RETRYABLE sentence and the catalogue move together.
- Known limit, read from the code and not exercised, not changed here: a unique violation inside the request transaction (the version-1 `INSERT` paths of
  `save` and `Policies.Save`) aborts the savepoint, so `s.tx`'s retry of that conflict runs on an aborted
  transaction and answers 500, which is never stored either. The guarded `UPDATE` paths (`RowsAffected` 0) do not
  abort and retry normally. Left to a follow-up.
