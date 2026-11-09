package repocontract

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
)

// lateRiskCases pin the DepartureArrival read model and the two promise
// listings of the late-risk sweep (ADR 0021).
var lateRiskCases = []named{
	{"a recorded arrival is read back", arrivalRoundTrip},
	{"a repeated arrival changes nothing", arrivalRepeat},
	{"rows past the transit window are pruned on write", arrivalPrune},
	{"an arrival joins the unit of work", arrivalUnitOfWork},
	{"ListConfirmed pages the CONFIRMED promises by id", promiseListConfirmed},
	{"ListAtRiskPage skips the offset in ListAtRisk order", promiseListAtRiskPage},
}

func arrivalRoundTrip(t *testing.T, f Fixture) {
	got, err := f.Arrivals.DepartureArrived(ctx, "dep-1800")
	noErr(t, err)
	eq(t, "before", got, false)
	noErr(t, f.Arrivals.RecordDepartureArrived(ctx, "dep-1800", T0))
	got, err = f.Arrivals.DepartureArrived(ctx, "dep-1800")
	noErr(t, err)
	eq(t, "after", got, true)
	got, err = f.Arrivals.DepartureArrived(ctx, "dep-2200")
	noErr(t, err)
	eq(t, "another departure", got, false)
}

func arrivalRepeat(t *testing.T, f Fixture) {
	noErr(t, f.Arrivals.RecordDepartureArrived(ctx, "dep-1800", T0))
	noErr(t, f.Arrivals.RecordDepartureArrived(ctx, "dep-1800", T0.Add(time.Hour)))
	got, err := f.Arrivals.DepartureArrived(ctx, "dep-1800")
	noErr(t, err)
	eq(t, "still there", got, true)
}

func arrivalPrune(t *testing.T, f Fixture) {
	window := time.Duration(policy.MaxTransitWindowDays) * 24 * time.Hour
	noErr(t, f.Arrivals.RecordDepartureArrived(ctx, "dep-old", T0.Add(-window-time.Hour)))
	noErr(t, f.Arrivals.RecordDepartureArrived(ctx, "dep-edge", T0.Add(-window)))
	noErr(t, f.Arrivals.RecordDepartureArrived(ctx, "dep-new", T0))
	for id, want := range map[string]bool{"dep-old": false, "dep-edge": true, "dep-new": true} {
		got, err := f.Arrivals.DepartureArrived(ctx, id)
		noErr(t, err)
		if got != want {
			t.Fatalf("%s arrived = %v, want %v", id, got, want)
		}
	}
}

func arrivalUnitOfWork(t *testing.T, f Fixture) {
	err := f.UoW.Do(ctx, func(c context.Context) error {
		noErr(t, f.Arrivals.RecordDepartureArrived(c, "dep-1800", T0))
		got, err := f.Arrivals.DepartureArrived(c, "dep-1800")
		noErr(t, err)
		eq(t, "visible inside the unit", got, true)
		return errBoom
	})
	wantErr(t, err, errBoom)
	got, err := f.Arrivals.DepartureArrived(ctx, "dep-1800")
	noErr(t, err)
	eq(t, "rolled back", got, false)
}

func promiseListConfirmed(t *testing.T, f Fixture) {
	for _, id := range []string{"prm-1", "prm-2", "prm-3"} {
		confirmedPromise(t, f, id, "off-"+id, "ord-"+id, 1)
	}
	noErr(t, f.Promises.Save(ctx, NewPromise(t, "prm-4", "off-4", "N-AUS3", "dep-1800"))) // PROVISIONAL
	risky := confirmedPromise(t, f, "prm-5", "off-5", "ord-5", 1)
	_, err := risky.FlagAtRisk("WAVE_CANCELLED", T0.Add(time.Hour))
	noErr(t, err)
	noErr(t, f.Promises.Save(ctx, risky)) // AT_RISK
	all, err := f.Promises.ListConfirmed(ctx, "", 10)
	noErr(t, err)
	eq(t, "only CONFIRMED, by id", idsString(all), "prm-1 prm-2 prm-3")
	page, err := f.Promises.ListConfirmed(ctx, "", 2)
	noErr(t, err)
	eq(t, "first page", idsString(page), "prm-1 prm-2")
	page, err = f.Promises.ListConfirmed(ctx, page[len(page)-1], 2)
	noErr(t, err)
	eq(t, "second page", idsString(page), "prm-3")
	page, err = f.Promises.ListConfirmed(ctx, "prm-3", 2)
	noErr(t, err)
	eq(t, "past the end", len(page), 0)
}

func promiseListAtRiskPage(t *testing.T, f Fixture) {
	for _, id := range []string{"prm-1", "prm-2", "prm-3"} {
		p := confirmedPromise(t, f, id, "off-"+id, "ord-"+id, 1)
		_, err := p.FlagAtRisk("WAVE_CANCELLED", T0.Add(time.Hour))
		noErr(t, err)
		noErr(t, f.Promises.Save(ctx, p))
	}
	confirmedPromise(t, f, "prm-4", "off-4", "ord-4", 1) // CONFIRMED: not listed
	first, err := f.Promises.ListAtRisk(ctx, 10)
	noErr(t, err)
	eq(t, "ListAtRisk", len(first), 3)
	for offset, want := range map[int]int{0: 2, 1: 2, 2: 1, 3: 0, 9: 0, -1: 2} {
		page, err := f.Promises.ListAtRiskPage(ctx, offset, 2)
		noErr(t, err)
		eq(t, "page size at offset", len(page), want)
		if len(page) > 0 {
			eq(t, "first id of the page", page[0], first[max(offset, 0)])
		}
	}
}

func idsString(ids []promise.ID) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += " "
		}
		out += string(id)
	}
	return out
}
