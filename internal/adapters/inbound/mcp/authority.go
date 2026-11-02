package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Authority levels (Agentic Standard v2, AS-1). Every registered tool
// declares exactly one of these in its Meta under authorityMetaKey; the
// fleet rule is agent-authority (warehouse-docs agents/fleet/agent-authority.md)
// and the context decision is docs/adr/0013. The MCP surface of this context
// is READ-ONLY (docs/adr/0007), so every tool is L1: a pure read of this
// context's own state that changes nothing.
const (
	// authorityMetaKey is the tools/list _meta key carrying a tool's level.
	authorityMetaKey = "com.warehouse/authority"
	// authorityL1 reads facts and changes nothing.
	authorityL1 = "L1"
	// authorityL2 is a pure recommendation: computes a proposal, changes nothing.
	authorityL2 = "L2"
	// authorityL3 mutates state; the caller is accountable.
	authorityL3 = "L3"
	// authorityL4 mutates state inside a bounded envelope an ADR grants.
	authorityL4 = "L4"
)

// authorityMeta is the Meta a tool registers to declare its authority level.
func authorityMeta(level string) mcp.Meta { return mcp.Meta{authorityMetaKey: level} }
