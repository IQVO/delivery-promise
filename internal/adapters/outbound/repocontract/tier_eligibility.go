package repocontract

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
)

// The NodeTierEligibility read model (ADR 0022).
var tierEligibilityCases = []named{
	{"a missing row is not found (the node is eligible)", tierEligibilityMiss},
	{"a row round-trips and Ineligible lists only the revoked ones in order", tierEligibilityRoundTrip},
	{"an older changedAt never replaces the row, an equal or later one does", tierEligibilityOrdering},
	{"an Apply inside a rolled-back unit of work leaves nothing", tierEligibilityRollback},
}

func tierRow(node, tier string, eligible bool, changedAt time.Time) readmodel.NodeTierEligibility {
	return readmodel.NodeTierEligibility{NodeID: node, Tier: tier, Eligible: eligible, ChangedAt: changedAt}
}

func tierEligibilityMiss(t *testing.T, f Fixture) {
	_, err := f.TierEligibility.Get(ctx, "N-AUS3", "NEXT_DAY")
	wantErr(t, err, ports.ErrNotFound)
	rows, err := f.TierEligibility.Ineligible(ctx)
	noErr(t, err)
	eq(t, "no ineligible rows", len(rows), 0)
}

func tierEligibilityRoundTrip(t *testing.T, f Fixture) {
	r := f.TierEligibility
	for _, row := range []readmodel.NodeTierEligibility{
		tierRow("N-B", "SAME_DAY", false, T0), tierRow("N-A", "NEXT_DAY", false, T0), tierRow("N-A", "SAME_DAY", true, T0),
		tierRow("N-A", "SAME_DAY", false, T0.Add(time.Minute)),
	} {
		applied, err := r.Apply(ctx, row)
		noErr(t, err)
		eq(t, "applied", applied, true)
	}
	got, err := r.Get(ctx, "N-A", "NEXT_DAY")
	noErr(t, err)
	eq(t, "node", got.NodeID, "N-A")
	eq(t, "tier", got.Tier, "NEXT_DAY")
	eq(t, "eligible", got.Eligible, false)
	eqTime(t, "changedAt", got.ChangedAt, T0)
	rows, err := r.Ineligible(ctx)
	noErr(t, err)
	eq(t, "ineligible count", len(rows), 3)
	for i, want := range [][2]string{{"N-A", "NEXT_DAY"}, {"N-A", "SAME_DAY"}, {"N-B", "SAME_DAY"}} {
		eq(t, "node order", rows[i].NodeID, want[0])
		eq(t, "tier order", rows[i].Tier, want[1])
	}
}

func tierEligibilityOrdering(t *testing.T, f Fixture) {
	r := f.TierEligibility
	applied, err := r.Apply(ctx, tierRow("N-A", "NEXT_DAY", false, T0))
	noErr(t, err)
	eq(t, "first applies", applied, true)

	applied, err = r.Apply(ctx, tierRow("N-A", "NEXT_DAY", true, T0.Add(-time.Second)))
	noErr(t, err)
	eq(t, "an older one is dropped", applied, false)
	got, err := r.Get(ctx, "N-A", "NEXT_DAY")
	noErr(t, err)
	eq(t, "still ineligible", got.Eligible, false)
	eqTime(t, "changedAt kept", got.ChangedAt, T0)

	applied, err = r.Apply(ctx, tierRow("N-A", "NEXT_DAY", true, T0))
	noErr(t, err)
	eq(t, "an equal one applies", applied, true)
	applied, err = r.Apply(ctx, tierRow("N-A", "NEXT_DAY", false, T0.Add(time.Hour)))
	noErr(t, err)
	eq(t, "a later one applies", applied, true)
	got, err = r.Get(ctx, "N-A", "NEXT_DAY")
	noErr(t, err)
	eq(t, "ineligible again", got.Eligible, false)
	eqTime(t, "changedAt moved", got.ChangedAt, T0.Add(time.Hour))
}

func tierEligibilityRollback(t *testing.T, f Fixture) {
	err := f.UoW.Do(ctx, func(ctx2 context.Context) error {
		if _, err := f.TierEligibility.Apply(ctx2, tierRow("N-A", "NEXT_DAY", false, T0)); err != nil {
			return err
		}
		return errBoom
	})
	wantErr(t, err, errBoom)
	_, err = f.TierEligibility.Get(ctx, "N-A", "NEXT_DAY")
	wantErr(t, err, ports.ErrNotFound)
}
