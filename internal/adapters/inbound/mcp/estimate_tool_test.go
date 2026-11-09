package mcp_test

import (
	"testing"
)

// estimate_delivery is the advisory listing-page estimate (docs/adr/0019):
// the same use case as POST /delivery-estimates, nothing stored.

func TestEstimateDelivery(t *testing.T) {
	h := newHarness(t)
	before := h.store.Counts()
	got := h.ok(t, "estimate_delivery", map[string]any{
		"destinationPostalPrefix": "787",
		"items": []any{
			map[string]any{"catalogItemId": "ci-1001", "quantity": 2},
			map[string]any{"catalogItemId": "ci-404", "quantity": 1},
		},
	})
	if after := h.store.Counts(); after != before {
		t.Fatalf("store changed %+v -> %+v: the estimate wrote", before, after)
	}
	if got["destinationPostalPrefix"] != "787" || got["computedAt"] == nil {
		t.Fatalf("answer = %v", got)
	}
	estimates, _ := got["estimates"].([]any)
	if len(estimates) != 2 {
		t.Fatalf("estimates = %v", got["estimates"])
	}
	first, second := estimates[0].(map[string]any), estimates[1].(map[string]any)
	if first["catalogItemId"] != "ci-1001" || len(first["options"].([]any)) == 0 {
		t.Fatalf("first = %v", first)
	}
	opt := first["options"].([]any)[0].(map[string]any)
	if opt["tier"] != "NEXT_DAY" || opt["basis"] != "SCHEDULED" || opt["window"].(map[string]any)["timeZone"] != "America/Chicago" {
		t.Fatalf("NEXT_DAY option = %v", opt)
	}
	constraints, _ := second["constraints"].([]any)
	if second["catalogItemId"] != "ci-404" || len(constraints) != 1 || constraints[0].(map[string]any)["code"] != "ITEM_QUANTITY_NOT_AVAILABLE" {
		t.Fatalf("unknown item = %v", second)
	}
}

func TestEstimateDeliveryTiersAndErrors(t *testing.T) {
	h := newHarness(t)
	got := h.ok(t, "estimate_delivery", map[string]any{
		"destinationPostalPrefix": "787", "tiers": []any{"TWO_DAY"},
		"items": []any{map[string]any{"catalogItemId": "ci-1001", "quantity": 1}},
	})
	options := got["estimates"].([]any)[0].(map[string]any)["options"].([]any)
	if len(options) != 1 || options[0].(map[string]any)["tier"] != "TWO_DAY" {
		t.Fatalf("options = %v", options)
	}
	dup := map[string]any{"catalogItemId": "ci-1001", "quantity": 1}
	failWith(t, h.session, "estimate_delivery", map[string]any{"destinationPostalPrefix": "787", "items": []any{dup, dup}}, "validation-failed")
	failWith(t, h.session, "estimate_delivery", map[string]any{"destinationPostalPrefix": "787", "items": []any{}}, "validation-failed")
	failWith(t, h.session, "estimate_delivery", map[string]any{
		"destinationPostalPrefix": "787", "tiers": []any{"EXPRESS"}, "items": []any{dup},
	}, "validation-failed")
}
