package search

import (
	"reflect"
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

const (
	scheduledBook = "N-AUS3:dep-AUS3-1800"
	nominalBook   = "N-AUS3:nominal-20261022T2300Z"
)

func bookWith(share, used int) BookView {
	return BookView{Open: true, Room: share - used, HoldDeadlineAt: at(22, 16, 25), Share: share, Used: used}
}

func bandOfBest(t *testing.T, in Input, tier shared.Tier) shared.CapacityBand {
	t.Helper()
	return best(t, onlyGroup(t, Run(in)), tier).CapacityBand
}

func TestCapacityBandOfAFreshBookIsAmple(t *testing.T) {
	if got := bandOfBest(t, fixture(), shared.TierNextDay); got != shared.BandAmple {
		t.Fatalf("a book still to be opened has nothing used: %q", got)
	}
}

func TestCapacityBandFollowsTheBookBoundaries(t *testing.T) {
	for _, c := range []struct {
		used int
		want shared.CapacityBand
	}{
		{0, shared.BandAmple}, {59, shared.BandAmple}, {60, shared.BandTight},
		{84, shared.BandTight}, {85, shared.BandScarce}, {99, shared.BandScarce},
	} {
		in := fixture()
		in.Books = map[string]BookView{scheduledBook: bookWith(100, c.used)}
		if got := bandOfBest(t, in, shared.TierNextDay); got != c.want {
			t.Errorf("used %d of 100: got %q want %q", c.used, got, c.want)
		}
	}
}

func TestCapacityBandIsOmittedWithoutFiniteCapacity(t *testing.T) {
	in := fixture()
	in.Books = map[string]BookView{scheduledBook: {Open: true, Room: 5, HoldDeadlineAt: at(22, 16, 25)}}
	if got := bandOfBest(t, in, shared.TierNextDay); got != "" {
		t.Fatalf("a view without a share carries no band: %q", got)
	}
}

func TestCapacityBandOfANominalBook(t *testing.T) {
	in := fixture()
	in.Waves, in.Departures = nil, nil
	in.Books = map[string]BookView{nominalBook: bookWith(40, 34)}
	c := best(t, onlyGroup(t, Run(in)), shared.TierStandard)
	if c.BookID != nominalBook || c.CapacityBand != shared.BandScarce {
		t.Fatalf("a nominal candidate reads its own book: %+v", c)
	}
}

// TestCapacityBandDoesNotChangeTheCandidates: two inputs whose books differ
// only in how much is used (the room is the same) yield the same candidates
// in the same order once the band is stripped.
func TestCapacityBandDoesNotChangeTheCandidates(t *testing.T) {
	quiet, busy := fixture(), fixture()
	quiet.Books = map[string]BookView{scheduledBook: {Open: true, Room: 40, HoldDeadlineAt: at(22, 16, 25)}}
	busy.Books = map[string]BookView{scheduledBook: bookWith(100, 60)}
	a, b := Run(quiet), Run(busy)
	if bandOfBest(t, busy, shared.TierNextDay) != shared.BandTight {
		t.Fatal("the busy input must carry a band")
	}
	if !reflect.DeepEqual(stripBands(a), stripBands(b)) {
		t.Fatalf("the band changed the candidates:\n%+v\n%+v", stripBands(a), stripBands(b))
	}
}

func stripBands(r Result) Result {
	out := Result{WholeQuote: r.WholeQuote}
	for _, g := range r.Groups {
		cands := map[shared.Tier][]Candidate{}
		for t, cs := range g.Candidates {
			for _, c := range cs {
				c.CapacityBand = ""
				cands[t] = append(cands[t], c)
			}
		}
		g.Candidates = cands
		out.Groups = append(out.Groups, g)
	}
	return out
}
