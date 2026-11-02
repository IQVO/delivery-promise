package a2a

import (
	"fmt"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// authorityTagPrefix is the pinned skill-tag form of a declaration (contracts
// G2): "authority:L1" .. "authority:L4".
const authorityTagPrefix = "authority:"

// skillAuthorityViolation returns why a skill breaks the authority rule, or
// "": exactly one authority:<L> tag with L in L1..L4; a skill tagged "write"
// must be L3 or L4, a read skill L1 or L2. Both skills of this agent are
// reads fronting L1 MCP tools (docs/adr/0010, docs/adr/0013).
func skillAuthorityViolation(s a2a.AgentSkill) string {
	var levels []string
	write := false
	for _, tag := range s.Tags {
		if lvl, ok := strings.CutPrefix(tag, authorityTagPrefix); ok {
			levels = append(levels, lvl)
		}
		if tag == "write" {
			write = true
		}
	}
	if len(levels) != 1 {
		return fmt.Sprintf("must carry exactly one %s<L> tag, got %v", authorityTagPrefix, levels)
	}
	level := levels[0]
	switch level {
	case "L1", "L2", "L3", "L4":
	default:
		return fmt.Sprintf("declares unknown authority level %q", level)
	}
	switch {
	case write && level != "L3" && level != "L4":
		return fmt.Sprintf("is tagged write so it must be L3 (L4 needs an ADR), got %s", level)
	case !write && level != "L1" && level != "L2":
		return fmt.Sprintf("is a read skill so it must be L1 or L2, got %s", level)
	}
	return ""
}

// TestEveryAgentToolDeclaresAuthority (A2A half; the MCP half lives in the
// mcp package under the same name) asserts every skill on the Agent Card
// declares exactly one valid authority level, the same level as the tool it
// fronts.
func TestEveryAgentToolDeclaresAuthority(t *testing.T) {
	card := Card(Config{AgentURL: testAgentURL, Version: "1.0.0"})
	if len(card.Skills) == 0 {
		t.Fatal("card has no skills; the sensor is looking at nothing")
	}
	want := map[string]string{skillPromiseStatus: "L1", skillBookCapacity: "L1"}
	for _, s := range card.Skills {
		if v := skillAuthorityViolation(s); v != "" {
			t.Errorf("skill %q %s", s.ID, v)
			continue
		}
		if want[s.ID] == "" {
			t.Errorf("skill %q is not part of the pinned surface", s.ID)
		} else if !containsTag(s.Tags, authorityTagPrefix+want[s.ID]) {
			t.Errorf("skill %q must declare authority %s (the level of the tool it fronts), tags = %v", s.ID, want[s.ID], s.Tags)
		}
	}
}
