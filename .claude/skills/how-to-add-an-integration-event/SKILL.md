---
name: how-to-add-an-integration-event
description: Publish or consume a cross-service Kafka event in this service: CloudEvents 1.0 type naming, AsyncAPI, the type catalogue ADR, the encoder, the transactional outbox, the inbound dispatch table. Use when touching internal/adapters kafka or outbox code, a domain event, or apis/asyncapi*.yaml.
---

# How to add an integration event

delivery-promise publishes twelve events (six on `.events`, six analytics-only on
`.analytics`) and consumes 34 (ADR 0003, ADR 0004). The envelope is CloudEvents 1.0
only; read `.claude/rules/integration-events.md` and
`.claude/rules/fleet/cloudevents.md` first. The worked example is `PromiseKept`.

## Publishing a new event

1. **Domain event.** A typed struct in `internal/domain/<pkg>/events.go`
   (`promise.PromiseKept`): embeds `shared.Header`, fields exactly the contract's
   `data` list, methods `EventName()`, `Entity()` (`promise`, `promisebook`,
   `deliveryoffer`, `promisepolicy`: lowercase, no separators) and `SubjectID()`
   (the aggregate id), plus a `var _ shared.Event = ...` assertion. The aggregate
   command returns it.
2. **Contract, one commit.** Add the message and its `data` schema to
   `apis/asyncapi.yaml`, AND one `type: com.retailnetwork.commerce.delivery-promise.<entity>.<Event>`
   line (with its stream) to `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`.
   `TestEventCatalogueMatchesContract` fails when the two disagree, so the push fails
   when they are not in the same commit. Lint:
   `npx --yes @stoplight/spectral-cli lint apis/asyncapi.yaml --ruleset .spectral.asyncapi.yaml`.
   Evolution is additive only; a breaking change is a new `:v2` dataschema.
3. **Encoder.** In `internal/adapters/outbound/kafka/encoder.go` add the event to
   `streamOf` (`events` for integration, `analytics` for analytics-only; the stream
   picks the topic) and a `case` in `payload` (camelCase keys, instants through `utc`,
   arrays never null). The envelope itself is built by `internal/adapters/kafka/cloudevents`
   only; never hand-build one. Add the event to `goldenCases()` in `encoder_test.go`
   and run `go test ./internal/adapters/outbound/kafka -update` once to write
   `testdata/<Event>.golden.json`, then read the file: it is the exact wire JSON.
4. **Raise it from the use case** inside the same `ports.UnitOfWork` as the `Save`
   (`s.Outbox.Append(ctx, events...)`): the aggregate row and the outbox row commit or
   roll back together. Never publish after the commit and never call Kafka from a use case.
5. **Nothing else.** The relay (`internal/adapters/outbound/outbox/relay.go`) already
   drains every row to `retailnetwork.delivery-promise.events` / `.analytics`.
6. **Prove it.** Extend `cmd/api/e2e_integration_test.go` when the event is part of a
   REST or Kafka flow; it reads the exact CloudEvents back from a real broker.
7. **Tell the consumers.** A published change is a contract change: name the event and
   field in the PR so the consuming context can re-read `apis/asyncapi.yaml`.

## Consuming an event from a sibling context

1. Re-read the producer's contract NOW:
   `git -C ~/warehouse-systems/<producer> show origin/feature/service:apis/asyncapi.yaml`.
   Copy the EXACT `type` string (the entity segment is the producer's, e.g.
   `packageattempthistory`, not a guess) and the field names.
2. Add the payload struct to `internal/application/usecases/events.go` (only the fields
   you rely on; tolerant decoding ignores the rest), the `On...` method to
   `projections.go` / `risk.go` / `judge.go` (it claims the CloudEvents id through
   `s.project`, so a redelivery commits nothing), and the handler line to
   `internal/adapters/inbound/kafka/dispatch.go` `Handlers`.
3. Add the `receive` message to `apis/asyncapi.yaml` and a row to the consumed table in
   `.claude/rules/integration-events.md` and ADR 0003.
   `TestHandlersAreExactlyTheConsumedMessagesOfTheContract` fails until the handler
   table and the contract agree.
4. A new topic goes into `Topics()` in `dispatch.go`. The consumer group is the single
   mandatory `DELIVERY_PROMISE_CONSUMER_GROUP`; never a literal.
5. Test the projection in `usecases` (memory doubles), the dispatch in
   `internal/adapters/inbound/kafka/dispatch_test.go`, and add the event to the
   producers' side of `cmd/api/e2e_integration_test.go` when it is part of a flow.

## Verify before opening the PR

`make check`, `go test ./internal/architecture/ -count=1` (catalogue, CloudEvents-only,
relay cap, DLQ), the spectral lint of the AsyncAPI, and
`go test -tags=integration ./cmd/... -race -count=1`.
