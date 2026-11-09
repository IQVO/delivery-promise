package usecases_test

import (
	"encoding/json"
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

const bandBookID = "N-AUS3:dep-AUS3-1800"

// seedBook stores the open book of the worked example's pickup with a share of
// 20 and the given provisional and confirmed holds.
func (e *env) seedBook(provisional, confirmed int) {
	e.t.Helper()
	b, err := book.Restore(book.Snapshot{
		ID: bandBookID, NodeID: "N-AUS3", PickupDepartureID: "dep-AUS3-1800", CutoffAt: at(22, 18, 0), HoldDeadlineAt: at(22, 16, 25),
		Share: 20, Provisional: provisional, Confirmed: confirmed, State: book.StateOpen, Version: 1,
	})
	must(e.t, err)
	must(e.t, e.store.Books().Save(ctx, b))
}

func TestAQuoteCarriesTheBandOfTheBookItsWindowCharges(t *testing.T) {
	for _, c := range []struct {
		name                   string
		provisional, confirmed int
		want                   shared.CapacityBand
	}{
		{"11 of 20 is 0.55", 7, 4, shared.BandAmple},
		{"12 of 20 is 0.60", 7, 5, shared.BandTight},
		{"16 of 20 is 0.80", 6, 10, shared.BandTight},
		{"17 of 20 is 0.85", 9, 8, shared.BandScarce},
	} {
		e := newEnv(t)
		e.seedBook(c.provisional, c.confirmed)
		o := offerOf(t, e.quote(), "NEXT_DAY")
		eq(t, c.name, shared.CapacityBand(o.CapacityBand), c.want)
	}
}

func TestAQuoteOnABookNotOpenedYetIsAmple(t *testing.T) {
	e := newEnv(t)
	o := offerOf(t, e.quote(), "NEXT_DAY")
	eq(t, "band", o.CapacityBand, "AMPLE")
}

// The band is a pure addition: the offers, their windows and their order are
// byte-identical once the field is stripped, whatever the book holds.
func TestTheBandChangesNothingButTheField(t *testing.T) {
	plain := newEnv(t).quote()
	busy := newEnv(t)
	busy.seedBook(10, 8)
	got := busy.quote()
	eq(t, "offers", len(got.Offers), len(plain.Offers))
	eq(t, "offered band", offerOf(t, got, "NEXT_DAY").CapacityBand, "SCARCE")
	eq(t, "stripped offers", stripBand(t, got.Offers), stripBand(t, plain.Offers))
	eq(t, "unavailable", marshal(t, got.Unavailable), marshal(t, plain.Unavailable))
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	must(t, err)
	return string(b)
}

// stripBand marshals the offers and removes every capacityBand member.
func stripBand(t *testing.T, offers any) string {
	t.Helper()
	var generic []map[string]any
	must(t, json.Unmarshal([]byte(marshal(t, offers)), &generic))
	for _, o := range generic {
		delete(o, "capacityBand")
	}
	return marshal(t, generic)
}

func TestTheQuotedEventCarriesTheBand(t *testing.T) {
	e := newEnv(t)
	e.seedBook(10, 8)
	e.quote()
	found := 0
	for _, ev := range e.store.Outbox().Events() {
		if q, ok := ev.(offer.DeliveryOfferQuoted); ok {
			found++
			eq(t, "event band", q.CapacityBand, shared.BandScarce)
		}
	}
	eq(t, "quoted events", found > 0, true)
}

func TestAnEstimateOptionCarriesTheSameBand(t *testing.T) {
	e := newEnv(t)
	e.seedBook(7, 5)
	before := e.store.Counts()
	est := e.estimate(lines())
	eq(t, "estimate band", optionOf(t, est.Estimates[0], "NEXT_DAY").CapacityBand, "TIGHT")
	eq(t, "estimate stored nothing", e.store.Counts(), before)
	eq(t, "quote band", offerOf(t, e.quote(), "NEXT_DAY").CapacityBand, "TIGHT")
}
