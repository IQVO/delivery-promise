---
name: architecture-review
description: Bounded-context boundary and ADR-compliance review of a change (expensive, post-integration): hexagonal direction, cross-context coupling, contradicted ADRs. Invoke explicitly: /architecture-review [range].
disable-model-invocation: true
argument-hint: "[git range]"
---

<!-- TEMPLATE NOTE (warehouse-harness-template v2): adapt every repo-specific example in this file (file paths, type names, field names) to THIS repo real code. Do not copy-paste verbatim. -->

Perform a bounded-context boundary and ADR-compliance review of the
current changes (or `$ARGUMENTS` if given, e.g. a branch/PR diff range).

This is EXPENSIVE relative to `/code-review` — it reasons about
cross-repo/cross-context implications, not just this diff's local
correctness. Use it post-integration (before merging a PR that touches
architecture, not on every small commit) or when asked explicitly to
check a design decision against this fleet's standing architecture.

## What to check, in priority order

1. **Hexagonal dependency direction.** Domain depends on nothing;
   application depends on domain+ports; adapters depend on
   application+domain; only `cmd/` wires every layer together. Run
   `go test ./internal/architecture/... -v` first — if it's already red,
   report that and stop; don't hand-review what a fitness test already
   caught.
2. **Customer/Supplier direction, per `.claude/rules/domain-model.md`
   (or this repo's equivalent doc).** A new outbound call to a sibling
   context must go the direction ADRs already established — check
   `docs/adr/` for the relevant context-mapping ADR before assuming
   a new integration is fine. Flag any outbound call added to a context
   this repo doesn't already integrate with; that's a new architectural
   decision that needs its own ADR, not a code change slipped in
   silently.
3. **MCP additive-boundary rule (ADR-0008 fleet-wide).** This service has no MCP adapter
   (`internal/architecture/mcp_optional_inputs_test.go` keeps one from appearing
   unannounced); any new inbound adapter must depend only on application/domain.
2b. **Zero-write guardrails, where applicable.** If this repo has a
   documented zero-write constraint (e.g. warehouse-ops-agent v1), check
   no mutating HTTP method or MCP tool without `ReadOnlyHint: true` was
   added to an outbound/inbound surface bound by that constraint.
4. **No analytics read side.** This service keeps no separate analytics store; the
   analytics-only events go to `retailnetwork.delivery-promise.analytics` through the
   same outbox. Flag any new store, topic or projector that is not in an ADR.
5. **Kafka consumer correctness.** The one consumer reads the eight integration topics
   under the single mandatory group `DELIVERY_PROMISE_CONSUMER_GROUP`, commits offsets
   only after the unit of work committed, dedupes on the CloudEvents id (inbox) and
   dead-letters to `<topic>.dlq` after bounded retries. Flag a second consumer, a
   handler that writes outside a unit of work, an auto-commit, or a group id literal:
   these are correctness bugs, not style.
6. **A new bounded-context integration with no companion documentation.**
   If this change introduces or changes a cross-repo contract (a new
   REST call, a new Kafka topic subscription, a new MCP tool consumed by
   a sibling), check whether an ADR documents the decision — and whether
   a companion ADR should exist in the OTHER repo too, per this fleet's
   contract-change convention (see `.claude/skills/how-to-write-an-adr/SKILL.md`).
7. **Auth-reintroduction and sibling-call bans**, same as `/code-review`
   items 6-8, but reasoned about more thoroughly here — check not just
   "is there a Bearer literal" but "does this change's INTENT require
   re-litigating the fleet-wide auth-removal decision," which would need
   its own ADR, not a silent code change.

## Output format

State clearly: PASS (no architectural concerns), CONCERNS (list them,
each tied to the specific rule/ADR it would violate), or NEEDS-ADR (the
change is architecturally sound but undocumented — name what the ADR
should cover). Cite the specific file/rule/ADR for every finding; a
finding with no citation is not actionable.

This is advisory. It never blocks a merge on its own, and it never
modifies files. If a finding conflicts with a decision explicitly stated
in this repo's own AGENTS.md/CLAUDE.md, defer to that document and say
so — this command reasons about the fleet's general conventions, not a
higher authority than the repo's own explicit guidance.
