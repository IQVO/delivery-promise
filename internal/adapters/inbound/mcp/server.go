// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a second driving adapter over the
// same application-layer read use cases the REST adapter uses. It is built on
// the official MCP Go SDK and served over Streamable HTTP only.
//
// Per docs/adr/0007-mcp-server-adoption.md the surface is READ-ONLY: every
// tool reads promise data (get_delivery_offer, list_promises_by_order,
// get_promise, list_promise_books, get_current_policy); quoting, making,
// confirming and cancelling a promise and publishing a policy stay on REST
// and on the event consumers, and governance_test.go fails the build on a
// write-verb tool name. This package depends inward on the application layer
// and the domain only, never on an outbound adapter or the REST adapter, and
// nothing else may depend on it (internal/architecture's
// TestMCPAdapterDependencyRule). The composition root (cmd/mcp) wires the
// concrete repositories into the use cases. There is no auth of any kind
// (fleet-wide revert 2026-09-11; TestNoAuthMiddlewareReintroduced).
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "delivery-promise-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Delivery promises of the retail network (read-only): read one short-lived delivery offer with " +
	"get_delivery_offer (its tier, window, witness path and state QUOTED, EXPIRED or CONSUMED); read the confirmed " +
	"promises of one customer order with list_promises_by_order (one per shipment group) or one promise in any state " +
	"with get_promise (its terms, witness path, revisions and judgement); see the capacity ledger of a node's pickup " +
	"cutoffs on one date with list_promise_books (share, provisional and confirmed holds per book); and read the " +
	"current promise policy with get_current_policy (offer TTL, hold TTL, packages per order, capacity share and " +
	"per-tier order deadlines). Nothing here changes data: quoting and the promise lifecycle are REST operations of " +
	"delivery-promise, driven by checkout, and the policy is published on REST too."

// NewServer builds the MCP server for this bounded context with every tool
// registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}
