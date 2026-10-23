---
name: how-to-write-an-adr
description: Write an architecture decision record for this service: numbering, sections, the immutable-body rule, the index and the assumptions list. Use when a change takes a decision that is not obvious from the code (a new dependency, a contract choice, a model gap).
---

# How to write an ADR

Use an ADR for a decision someone will later ask "why?" about: a contract choice, a
reading of a model gap, a new dependency, a runtime behaviour. Do not write one for a
change the code and its tests already explain.

## Numbering and location

`docs/adr/NNNN-kebab-case-title.md`, four-digit zero-padded and sequential: list
`docs/adr/` and take the next integer, never reuse or guess. Add a row to
`docs/adr/index.md` in the same commit. The ADR bodies are immutable; a changed
decision gets a NEW ADR that says which one it supersedes (and the old ADR's
`## Status` line is the only thing edited).

## Sections

```markdown
# ADR NNNN: <the decision, as a statement>

## Status

Accepted (YYYY-MM-DD).   <!-- or: Superseded by ADR MMMM -->

## Context

The forces: what the model or the contracts say, what they leave open, what
constraint applies.

## Decision

What we do, specific enough that a reader can check the code against it.

## Consequences

What this makes easy, what it costs, what it forecloses. Be honest about the
downsides: an ADR that only lists benefits reads as marketing.
```

## Assumptions where the model is silent

Every reading of a gap in `model/model.yaml`, the Gap Register or contracts-delta is
a numbered assumption (`A1`, `A2`, ...) in `docs/adr/0002-aggregates-and-invariants.md`
and is repeated in the final report as `model_gaps`. Continue the numbering; do not
renumber. Each assumption has a table-driven test in the package that enforces it.

## The CloudEvents catalogue ADR

`docs/adr/0004-cloudevents-envelope-and-type-catalogue.md` holds one `type:` line per
published type; `TestEventCatalogueMatchesContract` compares it with
`apis/asyncapi.yaml`. Change both in the same commit.

## Worked examples in this repo

ADR 0002 (aggregates, invariants and assumptions), ADR 0003 (consumed contracts and
local read models: states the mechanism and the relationship) and ADR 0005 (the outbox
relay and its attempt cap: states the trade-off). Write the Decision section like those:
the exact mechanism, not the intention.
