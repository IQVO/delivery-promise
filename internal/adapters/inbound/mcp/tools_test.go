package mcp_test

import (
	"testing"
)

// The five tools read back exactly what the write use cases persisted (the
// worked example: one NEXT_DAY promise for ord-1001 on N-AUS3's 18:00
// cutoff, policy v1), with the same camelCase wire shape as the REST
// contract. Structured content numbers arrive as float64.

func TestGetDeliveryOffer(t *testing.T) {
	h := newHarness(t)
	got := h.ok(t, "get_delivery_offer", map[string]any{"offerId": "off-1"})
	for k, v := range map[string]any{
		"offerId": "off-1", "state": "CONSUMED", "groupNo": 1.0, "destinationPostalPrefix": "787",
		"tier": "NEXT_DAY", "basis": "SCHEDULED", "policyVersion": "v0",
	} {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	witness, _ := got["witnessPath"].(map[string]any)
	if witness["nodeId"] != "N-AUS3" || witness["pickupDepartureId"] != "dep-AUS3-1800" || witness["deliveryWaveId"] != "W-DAU1-1023-A" {
		t.Fatalf("witnessPath = %v", witness)
	}
	lines, _ := got["lines"].([]any)
	if len(lines) != 1 || lines[0].(map[string]any)["catalogItemId"] != "ci-1001" {
		t.Fatalf("lines = %v", got["lines"])
	}
}

func TestGetDeliveryOfferErrors(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "get_delivery_offer", map[string]any{"offerId": "off-404"}, "not-found")
}

func TestGetPromise(t *testing.T) {
	h := newHarness(t)
	got := h.ok(t, "get_promise", map[string]any{"promiseId": "prm-1"})
	for k, v := range map[string]any{
		"promiseId": "prm-1", "offerId": "off-1", "state": "CONFIRMED", "orderId": "ord-1001",
		"groupNo": 1.0, "destinationPostalPrefix": "787", "version": 2.0,
	} {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	terms, _ := got["terms"].(map[string]any)
	if terms["tier"] != "NEXT_DAY" || terms["policyVersion"] != "v0" {
		t.Fatalf("terms = %v", terms)
	}
	if revs, _ := got["revisions"].([]any); len(revs) != 0 {
		t.Fatalf("revisions = %v, want none on a fresh promise", revs)
	}
	if _, has := got["judgement"]; has {
		t.Fatalf("judgement must be absent while the promise is undelivered: %v", got)
	}
}

func TestGetPromiseErrors(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "get_promise", map[string]any{"promiseId": "prm-404"}, "not-found")
}

func TestListPromisesByOrder(t *testing.T) {
	h := newHarness(t)
	got := h.ok(t, "list_promises_by_order", map[string]any{"orderId": "ord-1001"})
	items, _ := got["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v, want the one confirmed promise", got)
	}
	first := items[0].(map[string]any)
	if first["promiseId"] != "prm-1" || first["state"] != "CONFIRMED" || first["groupNo"] != 1.0 {
		t.Fatalf("item = %v", first)
	}
	if empty := h.ok(t, "list_promises_by_order", map[string]any{"orderId": "ord-2"}); len(empty["items"].([]any)) != 0 {
		t.Fatalf("an unknown order lists no promises: %v", empty)
	}
}

func TestListPromisesByOrderErrors(t *testing.T) {
	h := newHarness(t)
	// an order id with a slash is not a valid id: shared.ValidID rejects it
	failWith(t, h.session, "list_promises_by_order", map[string]any{"orderId": "ord/1"}, "validation-failed")
}

func TestListPromiseBooks(t *testing.T) {
	h := newHarness(t)
	got := h.ok(t, "list_promise_books", map[string]any{"nodeId": "N-AUS3", "date": "2026-10-22"})
	items, _ := got["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v, want the 18:00 cutoff book of N-AUS3", got)
	}
	b := items[0].(map[string]any)
	if b["bookId"] != "N-AUS3:dep-AUS3-1800" || b["nodeId"] != "N-AUS3" || b["state"] != "OPEN" {
		t.Fatalf("book = %v", b)
	}
	capacity, _ := b["capacity"].(map[string]any)
	if capacity["share"] != 2500.0 || capacity["confirmed"] != 1.0 || capacity["provisional"] != 0.0 {
		t.Fatalf("capacity = %v (2500 share, 1 confirmed hold)", capacity)
	}
	if none := h.ok(t, "list_promise_books", map[string]any{"nodeId": "N-AUS3", "date": "2026-10-23"}); len(none["items"].([]any)) != 0 {
		t.Fatalf("the 23rd has no cutoff: %v", none)
	}
}

func TestListPromiseBooksErrors(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "list_promise_books", map[string]any{"nodeId": "N/A", "date": "2026-10-22"}, "validation-failed")
	failWith(t, h.session, "list_promise_books", map[string]any{"nodeId": "N-AUS3", "date": "10/22/26"}, "validation-failed")
}

func TestGetCurrentPolicy(t *testing.T) {
	h := newHarness(t)
	got := h.ok(t, "get_current_policy", map[string]any{})
	for k, v := range map[string]any{
		"policyVersion": "v1", "version": 1.0, "offerTtlMinutes": 5.0, "holdTtlMinutes": 20.0,
		"packagesPerOrder": 2.0, "capacitySharePercent": 50.0,
	} {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	deadlines, _ := got["orderDeadlines"].([]any)
	if len(deadlines) != 1 {
		t.Fatalf("orderDeadlines = %v", got["orderDeadlines"])
	}
	d := deadlines[0].(map[string]any)
	if d["tier"] != "NEXT_DAY" || d["regionId"] != "R-SC" || d["localTime"] != "23:59" {
		t.Fatalf("deadline = %v", d)
	}
}

func TestReadsEmitNothing(t *testing.T) {
	h := newHarness(t)
	before := len(h.store.Outbox().Events())
	h.ok(t, "get_delivery_offer", map[string]any{"offerId": "off-1"})
	h.ok(t, "get_promise", map[string]any{"promiseId": "prm-1"})
	h.ok(t, "list_promises_by_order", map[string]any{"orderId": "ord-1001"})
	h.ok(t, "list_promise_books", map[string]any{"nodeId": "N-AUS3", "date": "2026-10-22"})
	h.ok(t, "get_current_policy", map[string]any{})
	if after := len(h.store.Outbox().Events()); after != before {
		t.Fatalf("events %d -> %d: a read tool emitted an event", before, after)
	}
}
