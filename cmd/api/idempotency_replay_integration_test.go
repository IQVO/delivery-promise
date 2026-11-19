//go:build integration

package main

// A refusal that a retry could pass is not stored under the Idempotency-Key
// (ADR 0031). Both tests drive the real router, the real middleware and a real
// Postgres; the first answer is a 409 whose problem type is RETRYABLE, and the
// proof is the database: no key row after it, the same key executing again,
// and the success stored and replayed afterwards.

import (
	"fmt"
	"net/http"
	"testing"
)

const retryPolicyBody = `{"offerTtlMinutes":10,"holdTtlMinutes":20}`

func TestAConcurrentUpdateRefusalReleasesTheKeySoTheRetryExecutes(t *testing.T) {
	w := newWorld(t)
	w.policies.contended.Store(true)

	loser := w.post("/promise-policies", "k-policy", retryPolicyBody)
	if loser.code != http.StatusConflict || loser.slug() != "concurrent-update" {
		t.Fatalf("the loser = %d %s, want 409 concurrent-update", loser.code, loser.body)
	}
	if n := w.keyRows("k-policy"); n != 0 {
		t.Fatalf("key rows after the loser = %d, want 0: the key must be released", n)
	}
	lostAttempts := w.policies.saves.Load()

	again := w.post("/promise-policies", "k-policy", retryPolicyBody)
	if again.code != http.StatusConflict || again.replayed() {
		t.Fatalf("the retry while the race is still lost = %d replayed=%v, want an executed 409", again.code, again.replayed())
	}
	if got := w.policies.saves.Load(); got <= lostAttempts {
		t.Fatalf("saves = %d after the retry, %d before: the retry did not execute", got, lostAttempts)
	}

	w.policies.contended.Store(false)
	won := w.post("/promise-policies", "k-policy", retryPolicyBody)
	if won.code != http.StatusCreated || won.replayed() {
		t.Fatalf("the retry once the race is won = %d replayed=%v %s, want a fresh 201", won.code, won.replayed(), won.body)
	}
	if n := w.keyRows("k-policy"); n != 1 {
		t.Fatalf("key rows after the success = %d, want 1: a 2xx is stored", n)
	}
	saves := w.policies.saves.Load()
	replay := w.post("/promise-policies", "k-policy", retryPolicyBody)
	if replay.code != http.StatusCreated || !replay.replayed() || replay.body != won.body {
		t.Fatalf("the third request = %d replayed=%v, want the stored 201 replayed byte for byte", replay.code, replay.replayed())
	}
	if w.policies.saves.Load() != saves {
		t.Fatal("the replay executed the use case again")
	}
}

func TestAnExhaustedBookRefusalReleasesTheKeySoTheSameKeySucceedsOnceCapacityIsFreed(t *testing.T) {
	w := newWorld(t)
	w.seedNetwork()
	first, second, third := w.nextDayOffer(), w.nextDayOffer(), w.nextDayOffer()

	held := w.post("/promises", "k-first", makePromiseBody(first))
	if held.code != http.StatusCreated {
		t.Fatalf("the first promise = %d %s, want 201 (it fills the book of share 1)", held.code, held.body)
	}
	promiseID := held.field("promiseId")
	confirm := w.post(fmt.Sprintf("/promises/%s/confirmations", promiseID), "k-confirm", `{"orderId":"ord-1","groupNo":1}`)
	if confirm.code != http.StatusOK {
		t.Fatalf("confirm = %d %s", confirm.code, confirm.body)
	}

	refused := w.post("/promises", "k-order-group", makePromiseBody(second))
	if refused.code != http.StatusConflict || refused.slug() != "promise-capacity-exhausted" {
		t.Fatalf("the second promise = %d %s, want 409 promise-capacity-exhausted", refused.code, refused.body)
	}
	if n := w.keyRows("k-order-group"); n != 0 {
		t.Fatalf("key rows after the refusal = %d, want 0: the key must be released", n)
	}
	if offer := w.get("/delivery-offers/" + second); offer.field("state") != "QUOTED" {
		t.Fatalf("the refused offer is %s, want it still QUOTED: the refusal left no trace", offer.field("state"))
	}

	// Still full: the same request under the same key runs again and is refused again, not replayed.
	again := w.post("/promises", "k-order-group", makePromiseBody(second))
	if again.code != http.StatusConflict || again.slug() != "promise-capacity-exhausted" || again.replayed() {
		t.Fatalf("the retry while the book is full = %d replayed=%v %s, want an executed 409", again.code, again.replayed(), again.body)
	}
	// A different offer under the released key is a new request, not a key reuse.
	other := w.post("/promises", "k-order-group", makePromiseBody(third))
	if other.slug() == "idempotency-key-reused" {
		t.Fatalf("another offer under a released key = %d %s: the key must be free", other.code, other.body)
	}

	// Capacity is given back: the confirmed promise is cancelled.
	cancel := w.post(fmt.Sprintf("/promises/%s/cancellations", promiseID), "k-cancel", ``)
	if cancel.code != http.StatusOK {
		t.Fatalf("cancel = %d %s", cancel.code, cancel.body)
	}

	made := w.post("/promises", "k-order-group", makePromiseBody(second))
	if made.code != http.StatusCreated || made.replayed() {
		t.Fatalf("the same key once capacity is free = %d replayed=%v %s, want a fresh 201", made.code, made.replayed(), made.body)
	}
	if n := w.keyRows("k-order-group"); n != 1 {
		t.Fatalf("key rows after the success = %d, want 1", n)
	}
	replay := w.post("/promises", "k-order-group", makePromiseBody(second))
	if replay.code != http.StatusCreated || !replay.replayed() || replay.body != made.body {
		t.Fatalf("the next request = %d replayed=%v, want the stored 201 replayed", replay.code, replay.replayed())
	}
}
