// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the fleet's agent ecosystem as a third driving adapter over the
// same application-layer read use cases the REST and MCP adapters use. It is
// built on the official A2A Go SDK (a2aproject/a2a-go) and speaks JSON-RPC 2.0
// over HTTP, with the Agent Card served at the well-known path.
//
// Per docs/adr/0010-a2a-inbound-adapter.md, A2A is the DELEGATION channel
// between agents: a client sends a goal-shaped skill request as a structured
// message part and this agent answers from its own state through its own use
// cases. It is not a data-sync channel -- facts between contexts still travel
// exclusively as CloudEvents on Kafka (docs/adr/0004).
//
// The surface is READ-ONLY, exactly like the MCP surface (docs/adr/0007): the
// write gate of the A2A rollout lets a skill change state only if MCP already
// offers that write, and MCP offers none. The adapter's Deps holds one Reads
// interface with three query methods and nothing that can write, and it
// imports no outbound adapter and no write use case. This package depends
// inward on the application layer and the domain only, and nothing else may
// depend on it (internal/architecture's TestA2AAdapterDependencyRule). The
// composition root (cmd/a2a) wires the concrete repositories into the use
// cases. There is no auth of any kind (fleet-wide revert 2026-09-11).
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/delivery-promise/internal/application/views"
)

// Reads is the read slice of the use cases: the SAME queries the REST adapter
// drives (GET /promises/{promiseId}, GET /promises?orderId=, GET
// /promise-books) and the MCP adapter exposes. *usecases.Service is the
// production implementation; the adapter holds no write use case at all, so
// this surface can never insert an outbox row or move an aggregate.
type Reads interface {
	GetPromise(ctx context.Context, id string) (views.Promise, error)
	ListPromisesByOrder(ctx context.Context, orderID string) (views.PromiseList, error)
	ListPromiseBooks(ctx context.Context, nodeID, date string) (views.BookList, error)
}

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL the fleet's agent clients can
// actually dial (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// Deps is everything the A2A skills need, injected by the composition root.
type Deps struct {
	Reads Reads
}

// Card builds this agent's Agent Card (docs/adr/0010). The surface is two
// curated, goal-shaped READ skills -- not one skill per endpoint, the same
// curation discipline docs/adr/0007 applied to MCP tools. The card declares
// no security schemes on purpose: this surface is unauthenticated by the same
// fleet-wide decision as REST and MCP.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "delivery-promise",
		Description: "Delivery Promise agent of the retail network (read-only): answers whether the delivery promises of a customer order, or one promise, are intact -- their state, terms, witness path, revisions and the KEPT or BROKEN judgement -- and how full a node's promise books are on a date (share, provisional and confirmed holds per pickup cutoff). It never quotes, makes, confirms, cancels or revises a promise and never publishes a policy; those stay on REST and the event consumers.",
		Version:     cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/delivery-promise",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:          skillPromiseStatus,
				Name:        "Status of the delivery promises of an order",
				Description: "Read the delivery promise(s) behind one customer order: pass orderId for every promise of the order (one per shipment group) or promiseId for one promise. Each promise carries its state (PROVISIONAL, CONFIRMED, AT_RISK, KEPT, BROKEN, RELEASED or CANCELLED), terms (tier, shipBy, deliverByEarliest, deliverByLatest, timeZone of the local calendar, basis, policyVersion), witness path, holdExpiresAt, revisions and, once judged, the judgement (outcome, cause, lateByMinutes). Pass exactly one of orderId and promiseId. Read-only; an unknown promise is answered as a 404 problem, an order with no promise as an empty list.",
				Tags:        []string{"delivery-promise", "promise", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"promise_status","arguments":{"orderId":"ord-1001"}}`,
					`{"skill":"promise_status","arguments":{"promiseId":"prm-1"}}`,
				},
			},
			{
				ID:          skillBookCapacity,
				Name:        "Promise book capacity of a node on a date",
				Description: "Read the promise books of one fulfillment node whose pickup cutoff falls on a date (YYYY-MM-DD, the node's own time zone): per book its state and capacity ledger (share, provisional and confirmed holds), its pickup departure and cutoffAt. Read-only; a node or date without books is answered as an empty list.",
				Tags:        []string{"delivery-promise", "capacity", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"book_capacity","arguments":{"nodeId":"N-AUS3","date":"2026-10-22"}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): both curated skills are request/response reads, so there is no
// long-running Task lifecycle to track. The response carries the caller's
// contextId (and taskId when the client referenced one).
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler -- the only transport this
// agent ships, mirroring docs/adr/0007's "Streamable HTTP only" minimalism for
// MCP.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
