package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// Reads is the read slice of the use cases: EXACTLY the six read use cases
// the REST adapter drives (GET /delivery-offers/{offerId}, /promises?orderId=,
// /promises/{promiseId}, /promise-books, /promise-policies/current and the
// safe POST /delivery-estimates, which stores nothing: docs/adr/0019).
// *usecases.Service is the production implementation; the adapter holds no
// write use case at all (docs/adr/0007), so this surface can never insert an
// outbox row or move an aggregate.
type Reads interface {
	EstimateDeliveries(ctx context.Context, cmd usecases.EstimateCommand) (views.EstimateResult, error)
	GetOffer(ctx context.Context, id string) (views.Offer, error)
	ListPromisesByOrder(ctx context.Context, orderID string) (views.PromiseList, error)
	GetPromise(ctx context.Context, id string) (views.Promise, error)
	ListPromiseBooks(ctx context.Context, nodeID, date string) (views.BookList, error)
	CurrentPolicy(ctx context.Context) (views.Policy, error)
}

// Deps is everything the MCP tools need, injected by the composition root.
// The answers are the application views the REST adapter returns, so both
// surfaces speak the identical camelCase wire shapes of apis/openapi.yaml.
type Deps struct {
	Reads Reads
}

// --- inputs -------------------------------------------------------------------

type offerIDInput struct {
	OfferID string `json:"offerId" jsonschema:"the delivery offer id returned by quote, e.g. off-1"`
}

type promiseIDInput struct {
	PromiseID string `json:"promiseId" jsonschema:"the promise id, e.g. prm-1"`
}

type orderInput struct {
	OrderID string `json:"orderId" jsonschema:"the customer order id (from CustomerOrderPlaced) whose confirmed promises to list, e.g. ord-1001"`
}

type listBooksInput struct {
	NodeID string `json:"nodeId" jsonschema:"the fulfillment node id whose books to list, e.g. N-AUS3"`
	Date   string `json:"date" jsonschema:"the cutoff date, YYYY-MM-DD, in the node's own time zone"`
}

type estimateItemInput struct {
	CatalogItemID string `json:"catalogItemId" jsonschema:"the catalogue item id, unique within the request, e.g. ci-1001"`
	Quantity      int    `json:"quantity" jsonschema:"the units of the item, 1 to 999"`
}

type estimateInput struct {
	DestinationPostalPrefix string              `json:"destinationPostalPrefix" jsonschema:"the destination postal prefix (never a full address), e.g. 787"`
	Items                   []estimateItemInput `json:"items" jsonschema:"the items to estimate, 1 to 50, each catalogItemId once"`
	Tiers                   []string            `json:"tiers,omitempty" jsonschema:"optional subset of SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD; all four when omitted"`
}

func (in estimateInput) command() usecases.EstimateCommand {
	cmd := usecases.EstimateCommand{DestinationPostalPrefix: in.DestinationPostalPrefix, Items: make([]shared.Line, len(in.Items))}
	for i, it := range in.Items {
		cmd.Items[i] = shared.Line{CatalogItemID: it.CatalogItemID, Quantity: it.Quantity}
	}
	for _, t := range in.Tiers {
		cmd.Tiers = append(cmd.Tiers, shared.Tier(t))
	}
	return cmd
}

// --- handlers -------------------------------------------------------------------

func (d Deps) estimateDelivery(ctx context.Context, in estimateInput) (views.EstimateResult, error) {
	out, err := d.Reads.EstimateDeliveries(ctx, in.command())
	if err != nil {
		return views.EstimateResult{}, mapError(ctx, err)
	}
	return out, nil
}

func (d Deps) getDeliveryOffer(ctx context.Context, in offerIDInput) (views.Offer, error) {
	out, err := d.Reads.GetOffer(ctx, in.OfferID)
	if err != nil {
		return views.Offer{}, mapError(ctx, err)
	}
	return out, nil
}

func (d Deps) getPromise(ctx context.Context, in promiseIDInput) (views.Promise, error) {
	out, err := d.Reads.GetPromise(ctx, in.PromiseID)
	if err != nil {
		return views.Promise{}, mapError(ctx, err)
	}
	return out, nil
}

func (d Deps) listPromisesByOrder(ctx context.Context, in orderInput) (views.PromiseList, error) {
	out, err := d.Reads.ListPromisesByOrder(ctx, in.OrderID)
	if err != nil {
		return views.PromiseList{}, mapError(ctx, err)
	}
	return out, nil
}

func (d Deps) listPromiseBooks(ctx context.Context, in listBooksInput) (views.BookList, error) {
	out, err := d.Reads.ListPromiseBooks(ctx, in.NodeID, in.Date)
	if err != nil {
		return views.BookList{}, mapError(ctx, err)
	}
	return out, nil
}

func (d Deps) getCurrentPolicy(ctx context.Context, _ struct{}) (views.Policy, error) {
	out, err := d.Reads.CurrentPolicy(ctx)
	if err != nil {
		return views.Policy{}, mapError(ctx, err)
	}
	return out, nil
}

// --- registry ------------------------------------------------------------------

// registerTools adds the six read-only tools. Every tool is annotated
// read-only, idempotent and closed-world; TestToolSurface pins the set and
// fails the build on any write-verb name (docs/adr/0007).
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false

	addTool(server, &mcp.Tool{
		Name: "get_delivery_offer",
		Description: "Read one delivery offer in any state (QUOTED, EXPIRED or CONSUMED): its tier, groupNo, " +
			"destination postal prefix, lines, delivery window (earliest, latest and the delivery station's IANA timeZone, " +
			"the local calendar of its days), the witness path that proves feasibility (node, pickup departure, legs, " +
			"delivery wave), basis (SCHEDULED from the timetable, MEASURED when a leg's measured transit pushed the " +
			"arrival later, or NOMINAL lane times), the policyVersion it was quoted under, quotedAt/expiresAt, " +
			"and orderByAt (the latest instant the order can be placed and still make the quoted window, uncapped by " +
			"the offer TTL). Offers are kept for a retention period after they expire. Read-only; an unknown offer " +
			"fails with not-found.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getDeliveryOffer)

	addTool(server, &mcp.Tool{
		Name: "get_promise",
		Description: "Read one promise in any state (PROVISIONAL, CONFIRMED, AT_RISK, KEPT, BROKEN, RELEASED or " +
			"CANCELLED): its terms (tier, shipBy, deliverByEarliest, deliverByLatest, timeZone, basis, policyVersion), the witness " +
			"path, holdExpiresAt, the revisions that moved it and the final judgement (outcome KEPT or BROKEN, cause, " +
			"lateByMinutes). A promise is never deleted; terminal states stay readable. Read-only; an unknown promise " +
			"fails with not-found.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getPromise)

	addTool(server, &mcp.Tool{
		Name: "list_promises_by_order",
		Description: "List the promises of one customer order, one per shipment group, ordered by groupNo: each with " +
			"its state, terms, witness path and judgement (the same shape as get_promise). Only CONFIRMED promises " +
			"belong to an order, so this is the checkout view of what was promised. Read-only; a malformed order id " +
			"fails with validation-failed.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.listPromisesByOrder)

	addTool(server, &mcp.Tool{
		Name: "list_promise_books",
		Description: "List the capacity ledger books of one fulfillment node whose pickup cutoff falls on one date " +
			"(YYYY-MM-DD in the node's own time zone), ordered by cutoffAt: one book per cutoff (bookId is " +
			"nodeId:pickupDepartureId), its OPEN or CLOSED state and its capacity (share, provisional and confirmed " +
			"holds). Read-only; a malformed node id or date fails with validation-failed.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.listPromiseBooks)

	addTool(server, &mcp.Tool{
		Name: "get_current_policy",
		Description: "Read the current promise policy: policyVersion (v0 is the built-in default when nothing was " +
			"published), offerTtlMinutes and holdTtlMinutes, packagesPerOrder, capacitySharePercent, the measured-transit " +
			"parameters (transitWindowDays of observations kept, transitServiceLevelPercent quoted against a lane's " +
			"measured transit, minTransitObservations before a lane beats the timetable) and the per-tier " +
			"order deadlines (localTime in the destination region's time zone). Publishing a policy is a REST " +
			"operation. Read-only; takes no arguments.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getCurrentPolicy)

	addTool(server, &mcp.Tool{
		Name: "estimate_delivery",
		Description: "Estimate the delivery options of up to 50 catalogue items for one destination postal prefix, as a " +
			"product listing page shows them: per item, in request order, the best option of every requested tier (tier, " +
			"window start/end with the delivery station's IANA timeZone, basis SCHEDULED, MEASURED or NOMINAL, and " +
			"orderByAt, the latest instant an order still makes that window) and the constraints of the tiers without one " +
			"(DESTINATION_NOT_SERVED or ITEM_QUANTITY_NOT_AVAILABLE for the whole item, otherwise DELIVERY_SLA_UNAVAILABLE, " +
			"NODE_CAPACITY_EXHAUSTED or PAST_ORDER_DEADLINE per tier). Each item is searched like a one-line quote, but " +
			"nothing is stored: no offer, no event, no hold. An estimate is advisory and never reserves capacity; only a " +
			"quote on REST backs a promise. An unknown item is a constraint, not an error; a bad prefix, tier, quantity, " +
			"duplicate item or more than 50 items fails with validation-failed.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.estimateDelivery)
}

// addTool registers one tool. A handler error is returned to the SDK as the
// handler's error, which the SDK turns into an isError tool result (never a
// transport/protocol failure), carrying the error text.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}
