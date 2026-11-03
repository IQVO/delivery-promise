# ADR 0013: agent authority levels and complete MCP surface

## Status

Accepted (2026-11-02).

## Context

The fleet adopted the Agentic Standard v2 (warehouse-docs `docs/architecture/agentic-standard.md`; fleet rule `agents/fleet/agent-authority.md`). Two of its requirements touch this context's agent surfaces directly:

- **AS-1, authority levels.** Every MCP tool and every A2A skill must declare exactly one authority level `L1`..`L4` (no L5) so a caller knows what gating to apply before invoking it: a read is `L1` (pure read of the context's own state) or `L2` (a computed recommendation that changes nothing), a write is `L3` unless an ADR grants `L4`. MCP declares it as a `mcp.Tool.Meta` entry under the key `com.warehouse/authority`; A2A declares it as a skill tag `authority:L1`..`authority:L4` (a write skill keeps its `write` tag).
- **AS-3, complete surface.** An MCP server that offers tools but no resource and no prompt is half a surface: resources give a host bounded context data without a tool call, prompts standardise an operator procedure across clients.
- **AS-4, stateless handler.** The Streamable HTTP handler must not rely on `Mcp-Session-Id`, so it also answers the 2026-07-28 protocol revision's per-request handshake (`server/discover`) while legacy `initialize` clients keep working.

This context's MCP charter is read-only ([ADR 0007](./0007-mcp-server-adoption.md)) and its A2A agent is read-only because MCP is ([ADR 0010](./0010-a2a-inbound-adapter.md)); the standard does not change that, it annotates and completes the existing surface.

## Decision

- **Every tool is `L1`.** The five MCP tools (`get_delivery_offer`, `get_promise`, `list_promises_by_order`, `list_promise_books`, `get_current_policy`) are pure reads of this context's own state and change nothing, so each registers `Meta: authorityMeta(authorityL1)` (the pinned fleet shape: consts `authorityMetaKey = "com.warehouse/authority"`, `authorityL1`..`authorityL4`, and `func authorityMeta(level string) mcp.Meta`, in `internal/adapters/inbound/mcp/authority.go`). There is no `L2` tool: nothing on the surface computes a recommendation, and quoting -- the closest thing -- is a write that stays on REST. There is no `L3`/`L4` tool: the charter remains read-only; an amendment of [ADR 0007](./0007-mcp-server-adoption.md) would precede any write tool. `checkToolMetadata` (governance) fails the build on a tool without `Meta[com.warehouse/authority] = "L1"`.
- **Both A2A skills carry `authority:L1`**, the level of the tool each fronts: `promise_status` (the `get_promise`/`list_promises_by_order` reads) and `book_capacity` (the `list_promise_books` read). They keep their `read` tags; neither is a write skill.
- **One resource, one prompt (AS-3).** `promise://books/{nodeId}/{date}` is a scoped read-model resource template backed by the same `ListPromiseBooks` use case as the tool and `GET /promise-books`, bounded to one node and one date, served as JSON text -- never a table dump. `check_order_promises` is the operator SOP prompt: how to read an order's promises and their judgements, when to consult the node's books, and when to escalate; it names tools but calls none, and hides no write (the surface has none). Resources and prompts are `L1` by nature and carry no tool Meta.
- **Stateless Streamable HTTP (AS-4).** `Handler` builds the handler with `&mcp.StreamableHTTPOptions{Stateless: true}`: no `Mcp-Session-Id` is issued, `server/discover` answers the 2026-07-28 revision, and a legacy `initialize` still answers 200 (verified by raw-POST tests in `stateless_test.go`, which also prove the authority key rides `tools/list` `_meta`, and fail if the options revert to nil). Nothing in the adapter relies on cross-call session state.
- **The fleet sensor guards it.** `internal/architecture/mcp_authority_fitness_test.go` (unmanaged copy of the warehouse-harness-template sensor) statically proves every `mcp.Tool` literal declares a readable level, every `a2a.AgentSkill` literal carries one `authority:Ln` tag, and the server registers a resource and a prompt. Runtime halves (`TestEveryAgentToolDeclaresAuthority` in both inbound adapters) assert the same over the live surfaces.
- **The golden carries the authority.** `testdata/tool_registry.golden.json` (the byte-for-byte pin of `tools/list`) now includes each tool's `_meta` authority; the fleet agent catalogue (warehouse-docs) derives levels offline from it.

No behaviour changes: same tool names, arguments, annotations and results; same skills; unknown or misspelt arguments stay rejected; CloudEvents untouched; no auth of any kind.

## Consequences

- An MCP client can gate calls by authority before invoking: everything here is `L1`, so no gating is ever more than advisory for this context.
- The catalogue generator (warehouse-docs `agent-catalogue.py`) can publish this server's levels from the golden without dialling it.
- Removing the authority Meta, a resource or the prompt now fails the build in two packages (architecture sensor + governance), not just review.
- An amendment of [ADR 0007](./0007-mcp-server-adoption.md) that adds an `L2` recommendation or an `L3` write tool must reuse the pinned constants and extend `wantTools`/the golden in the same PR; the sensor already accepts the shapes.
