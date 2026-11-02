package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// checkOrderPromisesSOP is the operational standard-operating-procedure the
// check_order_promises prompt hands to the model (Agentic Standard v2, AS-3;
// docs/adr/0013). Per the MCP charter (docs/adr/0007) the prompt encodes how
// to interpret results, when to escalate and what "done" means -- it names
// tools but never calls them, and hides no write: this surface has none.
const checkOrderPromisesSOP = `You are helping an operator judge whether the delivery promises of a customer order will be kept. Use only the MCP tools; never assume state. This context is read-only: quoting, making, confirming, cancelling or revising a promise and publishing a policy are REST operations driven by checkout and the event consumers -- if asked to change a promise, say so and stop.

Procedure:
1. Call list_promises_by_order(orderId) for the order. One promise per shipment group comes back ordered by groupNo; only CONFIRMED promises belong to an order. An empty list means the order has no promise yet.
2. For each promise read its state: PROVISIONAL (a hold, not yet confirmed), CONFIRMED, AT_RISK (atRiskReason says why), KEPT, BROKEN, RELEASED or CANCELLED. The judgement block (outcome KEPT or BROKEN, cause LATE, LOST or RETURNED, lateByMinutes) appears only once judged; the revisions list shows every term change and why.
3. For a promise still holding capacity, holdExpiresAt is when the hold lapses if checkout never confirms; get_current_policy() gives the TTLs behind it (offerTtlMinutes, holdTtlMinutes).
4. To see whether the node still had room, read the capacity ledger with list_promise_books(nodeId, date) -- the witnessPath of the promise names the node and its pickup departure; the date is the pickup cutoff date YYYY-MM-DD in the node's own time zone. share is the node's per-cutoff capacity, provisional and confirmed the holds against it.

Interpretation:
- AT_RISK means the feasible witness path degraded; read atRiskReason and the latest revision before drawing a conclusion. A later PromiseRevised revision may have already moved the terms.
- BROKEN is final, with a cause and lateByMinutes; KEPT is final. Neither can be undone from here.
- A book with confirmed holds at or near its share means the cutoff was nearly exhausted when the promise was made -- context for urgency, not a fault.

Escalate to a human when: a confirmed promise of an active order is AT_RISK or BROKEN and the order still ships, or a promise you expected is missing. Re-promising is never yours to do.

Done means: for every promise of the order you have reported its state and terms (tier, shipBy, deliverBy window in its own time zone), the judgement when there is one, any revision worth knowing, and a one-line verdict per promise: intact, at risk (why), or broken (cause, how late).`

// registerPrompts adds the workflow prompt (the operator SOP).
func (d Deps) registerPrompts(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "check_order_promises",
		Description: "Standard operating procedure for judging whether the delivery promises of a customer order are intact: read them and their judgements, read the capacity ledger behind them, and know when to escalate.",
	}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: "How to check the promises of an order: list them, read states and judgements, consult the node's books, escalate what is at risk or broken.",
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: checkOrderPromisesSOP},
			}},
		}, nil
	})
}
