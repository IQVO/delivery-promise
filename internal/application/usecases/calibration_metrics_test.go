package usecases_test

import (
	"context"
	"testing"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// policyInput is a policy with the given calibration minimum, target and cap.
func policyInput(minObs, target, maxBuffer int) policy.Input {
	return policy.Input{
		OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100,
		MinCalibrationObservations: minObs, TargetOnTimeProbabilityPercent: target, MaxCalibrationBufferMinutes: &maxBuffer,
	}
}

// calibrationRecorder is PromiseMetrics and CalibrationMetrics in one.
type calibrationRecorder struct {
	buffers map[string][]int
	thin    []string
}

func (*calibrationRecorder) PromiseMade(context.Context)                         {}
func (*calibrationRecorder) PromiseRefused(context.Context, string)              {}
func (*calibrationRecorder) TierCandidatesExcluded(context.Context, string, int) {}
func (*calibrationRecorder) TierEligibilityIgnored(context.Context, string)      {}
func (r *calibrationRecorder) CalibrationBufferApplied(_ context.Context, tier string, minutes int) {
	r.buffers[tier] = append(r.buffers[tier], minutes)
}
func (r *calibrationRecorder) CalibrationSegmentBelowMinimum(_ context.Context, tier, basis, grade string) {
	r.thin = append(r.thin, tier+"/"+basis+"/"+grade)
}

func TestAQuoteRecordsTheBufferPerTierAndTheThinSegments(t *testing.T) {
	e := newEnv(t)
	e.withSecondWave()
	rec := &calibrationRecorder{buffers: map[string][]int{}}
	e.seedSegment(twoDayScheduled(), 55, 5, 400)
	e.seedSegment(nextDayScheduled(search.GradeProvisional), 0, 7, 30) // seen, but below the minimum
	d := e.deps()
	d.Calibration, d.Metrics = e.store.Calibration(), rec
	_, err := usecases.New(d).QuoteDeliveryOffers(ctx, usecases.QuoteCommand{
		DestinationPostalPrefix: "787", Lines: lines(), Tiers: []shared.Tier{shared.TierNextDay, shared.TierTwoDay},
	})
	must(t, err)
	if got := rec.buffers["TWO_DAY"]; len(got) != 1 || got[0] != 400 {
		t.Fatalf("TWO_DAY buffers = %v, want [400]", got)
	}
	if got := rec.buffers["NEXT_DAY"]; len(got) != 1 || got[0] != 0 {
		t.Fatalf("NEXT_DAY buffers = %v, want [0] (the thin segment adds none)", got)
	}
	if len(rec.thin) != 1 || rec.thin[0] != "NEXT_DAY/SCHEDULED/PROVISIONAL" {
		t.Fatalf("thin segments = %v", rec.thin)
	}
}
