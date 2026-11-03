// Agentic Standard v2 (AS-1): every MCP tool declares exactly one authority
// level. A tool that forgets, or that mutates state while claiming a read
// level, fails the build. This is the runtime-wired half over the real
// server; the static fleet sensor lives in internal/architecture.
package mcp_test

import (
	"fmt"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// authorityOf returns the level a tool declares and whether it is a valid
// level string (L1..L4).
func authorityOf(tool *sdk.Tool) (string, bool) {
	level, ok := tool.Meta[authorityKey].(string)
	if !ok {
		return "", false
	}
	switch level {
	case "L1", "L2", "L3", "L4":
		return level, true
	}
	return level, false
}

// authorityViolation returns why tool breaks the authority rule, or "".
// The MCP charter of this context is READ-ONLY (docs/adr/0007), so a tool
// is read-only here and must be L1 or L2; L4 is never granted.
func authorityViolation(tool *sdk.Tool) string {
	level, ok := authorityOf(tool)
	if !ok {
		return fmt.Sprintf("must declare exactly one authority level L1..L4 in Meta[%q], got %v", authorityKey, tool.Meta[authorityKey])
	}
	readOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint
	switch {
	case !readOnly && level != "L3":
		return fmt.Sprintf("has ReadOnlyHint=false so it must be L3 (L4 is forbidden here), got %s", level)
	case readOnly && level != "L1" && level != "L2":
		return fmt.Sprintf("is read-only so it must be L1 or L2, got %s", level)
	}
	return ""
}

// TestEveryAgentToolDeclaresAuthority (MCP half; the A2A half lives in the
// a2a package under the same name) lists EVERY tool over the real server
// and asserts: exactly one valid level per tool; every tool whose
// ReadOnlyHint is false is L3; every read-only tool is L1 or L2. There are
// no conditional tools on this surface (TestToolSurface pins the set of
// five), so one listing covers all.
func TestEveryAgentToolDeclaresAuthority(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("no tools listed; the sensor is looking at nothing")
	}
	for _, tool := range res.Tools {
		if v := authorityViolation(tool); v != "" {
			t.Errorf("tool %q %s", tool.Name, v)
		}
	}
}

// TestAuthoritySensorFailsOnBadTools proves the sensor can fail: a missing
// declaration, a bad value, an L4 write and a write declared read-level are
// all rejected, and well-formed tools are accepted.
func TestAuthoritySensorFailsOnBadTools(t *testing.T) {
	ro := &sdk.ToolAnnotations{ReadOnlyHint: true}
	rw := &sdk.ToolAnnotations{ReadOnlyHint: false}
	cases := []struct {
		name string
		tool *sdk.Tool
		bad  bool
	}{
		{"read tool L1", &sdk.Tool{Name: "get_x", Annotations: ro, Meta: sdk.Meta{authorityKey: "L1"}}, false},
		{"read tool L2", &sdk.Tool{Name: "propose_x", Annotations: ro, Meta: sdk.Meta{authorityKey: "L2"}}, false},
		{"write tool L3", &sdk.Tool{Name: "assign_x", Annotations: rw, Meta: sdk.Meta{authorityKey: "L3"}}, false},
		{"no declaration", &sdk.Tool{Name: "get_x", Annotations: ro}, true},
		{"bad level", &sdk.Tool{Name: "get_x", Annotations: ro, Meta: sdk.Meta{authorityKey: "L5"}}, true},
		{"read level on a write tool", &sdk.Tool{Name: "assign_x", Annotations: rw, Meta: sdk.Meta{authorityKey: "L1"}}, true},
		{"L4 write", &sdk.Tool{Name: "assign_x", Annotations: rw, Meta: sdk.Meta{authorityKey: "L4"}}, true},
	}
	for _, tc := range cases {
		if v := authorityViolation(tc.tool); (v != "") != tc.bad {
			t.Errorf("%s: violation = %q, want bad=%v", tc.name, v, tc.bad)
		}
	}
}
