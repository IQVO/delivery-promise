package mcp_test

import "testing"

// capacityBand (ADR 0023) is a quote-time reading: estimate_delivery carries it
// on each option, get_delivery_offer (a stored offer) never does.

func TestEstimateDeliveryOptionsCarryACapacityBand(t *testing.T) {
	h := newHarness(t)
	got := h.ok(t, "estimate_delivery", map[string]any{
		"destinationPostalPrefix": "787",
		"items":                   []any{map[string]any{"catalogItemId": "ci-1001", "quantity": 2}},
	})
	estimates, _ := got["estimates"].([]any)
	options, _ := estimates[0].(map[string]any)["options"].([]any)
	if len(options) == 0 {
		t.Fatalf("no options in %v", got)
	}
	for _, o := range options {
		band, ok := o.(map[string]any)["capacityBand"].(string)
		if !ok || (band != "AMPLE" && band != "TIGHT" && band != "SCARCE") {
			t.Errorf("option %v: capacityBand = %q", o, band)
		}
	}
}

func TestGetDeliveryOfferOmitsTheCapacityBand(t *testing.T) {
	h := newHarness(t)
	got := h.ok(t, "get_delivery_offer", map[string]any{"offerId": "off-1"})
	if v, present := got["capacityBand"]; present {
		t.Fatalf("a stored offer has no band, got %v", v)
	}
}
