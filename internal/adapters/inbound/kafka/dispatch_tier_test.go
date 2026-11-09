package kafka

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
)

// tierEligibilityChanged is the FULL type of node-performance's event (ADR 0022).
const tierEligibilityChanged = "com.retailnetwork.partner.node-performance.scorecard.NodeTierEligibilityChanged"

// eligibilityData is the producer's data, with its extra fields: the consumer
// ignores what it does not read.
func eligibilityData(node, tier string, eligible bool, changedAt time.Time) map[string]any {
	return map[string]any{
		"nodeId": node, "tier": tier, "eligible": eligible, "metric": "onTimeHandoverRate", "value": 0.87,
		"threshold": 0.90, "window": "7d", "changedAt": changedAt, "someFutureField": []int{1},
	}
}

func tierConsumer(t *testing.T) (*Consumer, ports.NodeTierEligibility, *fakeDLQ) {
	t.Helper()
	svc, store := newService(nil)
	dlq := &fakeDLQ{}
	return &Consumer{Name: "t", Handlers: Handlers(svc), DLQ: dlq, Logger: discard()}, store.TierEligibility(), dlq
}

func TestTheTierEligibilityEventIsDispatchedOnItsFullTypeAndUnknownFieldsAreIgnored(t *testing.T) {
	c, rm, _ := tierConsumer(t)
	ctx := context.Background()
	if !slices.Contains(Topics(), NodePerformanceTopic) {
		t.Fatal("the node-performance topic is consumed already: no new subscription")
	}
	noErr(t, c.HandleMessage(ctx, message(t, "evt-1", tierEligibilityChanged, eligibilityData("N-A", "NEXT_DAY", false, t0))))
	got, err := rm.Get(ctx, "N-A", "NEXT_DAY")
	noErr(t, err)
	if got.Eligible || !got.ChangedAt.Equal(t0) {
		t.Fatalf("row = %+v", got)
	}
	noErr(t, c.HandleMessage(ctx, message(t, "evt-2", tierEligibilityChanged, eligibilityData("N-A", "SAME_DAY", false, t0))))
	rows, err := rm.Ineligible(ctx)
	noErr(t, err)
	if len(rows) != 2 {
		t.Fatalf("one event per tier, two rows: %+v", rows)
	}
}

func TestOnlyTheFullTypeStringIsDispatched(t *testing.T) {
	c, rm, _ := tierConsumer(t)
	ctx := context.Background()
	for i, typ := range []string{
		"com.retailnetwork.logistics.node-performance.scorecard.NodeTierEligibilityChanged", // wrong tier segment
		"com.retailnetwork.partner.node-performance.scorecard.NodeTierEligibilityChanged2",
		"com.retailnetwork.partner.node-performance.scorecard.NodeTierEligibility",
		"com.retailnetwork.partner.node-performance.tierstate.NodeTierEligibilityChanged", // wrong entity
	} {
		noErr(t, c.HandleMessage(ctx, message(t, "evt-x"+string(rune('a'+i)), typ, eligibilityData("N-A", "NEXT_DAY", false, t0))))
	}
	if _, err := rm.Get(ctx, "N-A", "NEXT_DAY"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("a near-miss type string must not be dispatched, got %v", err)
	}
}

func TestARedeliveredEligibilityEventChangesNothing(t *testing.T) {
	c, rm, _ := tierConsumer(t)
	ctx := context.Background()
	revoke := message(t, "evt-once", tierEligibilityChanged, eligibilityData("N-A", "NEXT_DAY", false, t0))
	noErr(t, c.HandleMessage(ctx, revoke))
	noErr(t, c.HandleMessage(ctx, message(t, "evt-back", tierEligibilityChanged, eligibilityData("N-A", "NEXT_DAY", true, t0.Add(time.Hour)))))
	noErr(t, c.HandleMessage(ctx, revoke)) // the inbox drops the replay
	got, err := rm.Get(ctx, "N-A", "NEXT_DAY")
	noErr(t, err)
	if !got.Eligible || !got.ChangedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("a replay must not undo a later fact: %+v", got)
	}
}

func TestAStaleChangedAtIsDroppedButAnEqualOneApplies(t *testing.T) {
	c, rm, _ := tierConsumer(t)
	ctx := context.Background()
	noErr(t, c.HandleMessage(ctx, message(t, "evt-new", tierEligibilityChanged, eligibilityData("N-A", "NEXT_DAY", false, t0.Add(time.Hour)))))
	noErr(t, c.HandleMessage(ctx, message(t, "evt-old", tierEligibilityChanged, eligibilityData("N-A", "NEXT_DAY", true, t0))))
	got, err := rm.Get(ctx, "N-A", "NEXT_DAY")
	noErr(t, err)
	if got.Eligible || !got.ChangedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("an older event arrived late and must lose: %+v", got)
	}
	noErr(t, c.HandleMessage(ctx, message(t, "evt-eq", tierEligibilityChanged, eligibilityData("N-A", "NEXT_DAY", true, t0.Add(time.Hour)))))
	if got, _ = rm.Get(ctx, "N-A", "NEXT_DAY"); !got.Eligible {
		t.Fatalf("an equal changedAt applies: %+v", got)
	}
}

func TestAnUnusableEligibilityEventIsIgnoredNotDeadLettered(t *testing.T) {
	c, rm, dlq := tierConsumer(t)
	ctx := context.Background()
	noTier := eligibilityData("N-A", "NEXT_DAY", false, t0)
	delete(noTier, "tier")
	noEligible := eligibilityData("N-A", "NEXT_DAY", false, t0)
	delete(noEligible, "eligible")
	noNode := eligibilityData("", "NEXT_DAY", false, t0)
	noChangedAt := eligibilityData("N-A", "NEXT_DAY", false, t0)
	delete(noChangedAt, "changedAt")
	for i, data := range []map[string]any{
		eligibilityData("N-A", "TWO_DAY", false, t0), eligibilityData("N-A", "STANDARD", false, t0), eligibilityData("N-A", "", false, t0),
		noTier, noEligible, noNode, noChangedAt,
	} {
		if err := c.HandleMessage(ctx, message(t, "evt-bad"+string(rune('a'+i)), tierEligibilityChanged, data)); err != nil {
			t.Fatalf("case %d: an unusable event is ignored, got %v", i, err)
		}
	}
	if len(dlq.got) != 0 {
		t.Fatalf("nothing may reach the dead-letter topic: %d messages", len(dlq.got))
	}
	rows, err := rm.Ineligible(ctx)
	noErr(t, err)
	if len(rows) != 0 {
		t.Fatalf("nothing may be stored: %+v", rows)
	}
}
