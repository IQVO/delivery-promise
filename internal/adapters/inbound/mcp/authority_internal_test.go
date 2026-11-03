package mcp

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The pinned declaration shape (contracts G2; the fleet sensor in
// internal/architecture reads exactly this): the key, the four level
// strings, and the helper that sets the key. Every level stays declared so
// a future L2 recommendation or L3 write tool on this surface (an ADR 0007
// amendment) reuses the same constants.
func TestAuthorityDeclarationShapeIsPinned(t *testing.T) {
	if authorityMetaKey != "com.warehouse/authority" {
		t.Errorf("authorityMetaKey = %q, want com.warehouse/authority", authorityMetaKey)
	}
	for level, want := range map[string]string{
		authorityL1: "L1", authorityL2: "L2", authorityL3: "L3", authorityL4: "L4",
	} {
		if level != want {
			t.Errorf("level const = %q, want %q", level, want)
		}
		if got := authorityMeta(level); got[authorityMetaKey] != want || len(got) != 1 {
			t.Errorf("authorityMeta(%q) = %v, want exactly {%q: %q}", level, got, authorityMetaKey, want)
		}
	}
	if got := authorityMeta(authorityL1); !sameMeta(got, mcp.Meta{authorityMetaKey: "L1"}) {
		t.Errorf("authorityMeta(L1) = %v", got)
	}
}

func sameMeta(a, b mcp.Meta) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
