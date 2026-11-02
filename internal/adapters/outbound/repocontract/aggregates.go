package repocontract

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/repository"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

var offerCases = []named{
	{"a saved offer round-trips", offerRoundTrip},
	{"a miss is ErrNotFound", offerMiss},
	{"a duplicate create and a stale update are ErrConcurrentUpdate", offerConcurrency},
	{"ListExpired returns only QUOTED offers past expiry, oldest first, up to the limit", offerListExpired},
}

func offerRoundTrip(t *testing.T, f Fixture) {
	o := NewOffer(t, "off-1")
	noErr(t, f.Offers.Save(ctx, o))
	got, err := f.Offers.Get(ctx, "off-1")
	noErr(t, err)
	eq(t, "state", got.State(), offer.StateQuoted)
	eq(t, "tier", got.Tier(), shared.TierNextDay)
	eq(t, "version", got.Version(), int64(1))
	eq(t, "witness node", got.Witness().NodeID, "N-AUS3")
	eq(t, "legs", len(got.Witness().Legs), 1)
	eq(t, "lines", got.Lines()[0].Quantity, 2)
	eq(t, "policy", got.PolicyVersion(), "v0")
	eqTime(t, "expiresAt", got.ExpiresAt(), o.ExpiresAt())
	eqTime(t, "pickup cutoff", got.PickupCutoffAt(), o.PickupCutoffAt())
	eqTime(t, "window latest", got.Window().Latest, o.Window().Latest)
	eq(t, "window time zone", got.Window().TimeZone, "America/Chicago")
	noErr(t, got.Consume(T0.Add(time.Minute)))
	noErr(t, f.Offers.Save(ctx, got))
	again, err := f.Offers.Get(ctx, "off-1")
	noErr(t, err)
	eq(t, "consumed state", again.State(), offer.StateConsumed)
	eqTime(t, "closedAt", again.ClosedAt(), T0.Add(time.Minute))
}

func offerMiss(t *testing.T, f Fixture) {
	_, err := f.Offers.Get(ctx, "off-nope")
	wantErr(t, err, ports.ErrNotFound)
}

func offerConcurrency(t *testing.T, f Fixture) {
	noErr(t, f.Offers.Save(ctx, NewOffer(t, "off-1")))
	wantErr(t, f.Offers.Save(ctx, NewOffer(t, "off-1")), ports.ErrConcurrentUpdate)
	a, _ := f.Offers.Get(ctx, "off-1")
	b, _ := f.Offers.Get(ctx, "off-1")
	noErr(t, a.Consume(T0))
	noErr(t, f.Offers.Save(ctx, a))
	noErr(t, b.Expire(T0.Add(time.Hour)))
	wantErr(t, f.Offers.Save(ctx, b), ports.ErrConcurrentUpdate)
	ghost := NewOffer(t, "off-ghost")
	noErr(t, ghost.Consume(T0))
	wantErr(t, f.Offers.Save(ctx, ghost), ports.ErrConcurrentUpdate)
}

func offerListExpired(t *testing.T, f Fixture) {
	for _, id := range []string{"off-1", "off-2", "off-3", "off-4"} {
		noErr(t, f.Offers.Save(ctx, NewOffer(t, id)))
	}
	consumed, _ := f.Offers.Get(ctx, "off-3")
	noErr(t, consumed.Consume(T0))
	noErr(t, f.Offers.Save(ctx, consumed))
	got, err := f.Offers.ListExpired(ctx, T0.Add(10*time.Minute), 10)
	noErr(t, err)
	eq(t, "expired ids", len(got), 3)
	eq(t, "first", got[0], offer.ID("off-1"))
	eq(t, "last", got[2], offer.ID("off-4"))
	got, _ = f.Offers.ListExpired(ctx, T0.Add(10*time.Minute), 2)
	eq(t, "limited", len(got), 2)
	got, _ = f.Offers.ListExpired(ctx, T0.Add(10*time.Minute-time.Nanosecond), 10)
	eq(t, "none before expiry", len(got), 0)
}

var bookCases = []named{
	{"a saved book round-trips", bookRoundTrip},
	{"a miss is ErrNotFound and stale saves are ErrConcurrentUpdate", bookConcurrency},
	{"List filters by node and cutoff range and orders by cutoff", bookList},
	{"ListOpenDue returns OPEN books at or past their cutoff", bookOpenDue},
}

func bookRoundTrip(t *testing.T, f Fixture) {
	noErr(t, f.Books.Save(ctx, NewBook(t, "N-AUS3", "dep-1800", 5)))
	b, err := f.Books.Get(ctx, "N-AUS3:dep-1800")
	noErr(t, err)
	_, err = b.Hold(T0)
	noErr(t, err)
	noErr(t, f.Books.Save(ctx, b))
	noErr(t, b.ConfirmHold())
	noErr(t, f.Books.Save(ctx, b))
	_, err = b.Hold(T0)
	noErr(t, err)
	noErr(t, f.Books.Save(ctx, b))
	got, err := f.Books.Get(ctx, "N-AUS3:dep-1800")
	noErr(t, err)
	eq(t, "share", got.Share(), 5)
	eq(t, "provisional", got.Provisional(), 1)
	eq(t, "confirmed", got.Confirmed(), 1)
	eq(t, "state", got.State(), book.StateOpen)
	eq(t, "version", got.Version(), int64(4))
	eqTime(t, "cutoff", got.CutoffAt(), b.CutoffAt())
	eqTime(t, "hold deadline", got.HoldDeadlineAt(), b.HoldDeadlineAt())
	noErr(t, got.Close(got.CutoffAt()))
	noErr(t, f.Books.Save(ctx, got))
	closed, _ := f.Books.Get(ctx, "N-AUS3:dep-1800")
	eq(t, "closed state", closed.State(), book.StateClosed)
	eqTime(t, "closedAt", closed.ClosedAt(), got.CutoffAt())
}

func bookConcurrency(t *testing.T, f Fixture) {
	_, err := f.Books.Get(ctx, "x:y")
	wantErr(t, err, ports.ErrNotFound)
	noErr(t, f.Books.Save(ctx, NewBook(t, "N-AUS3", "dep-1800", 5)))
	wantErr(t, f.Books.Save(ctx, NewBook(t, "N-AUS3", "dep-1800", 5)), ports.ErrConcurrentUpdate)
	a, _ := f.Books.Get(ctx, "N-AUS3:dep-1800")
	b, _ := f.Books.Get(ctx, "N-AUS3:dep-1800")
	_, _ = a.Hold(T0)
	noErr(t, f.Books.Save(ctx, a))
	_, _ = b.Hold(T0)
	wantErr(t, f.Books.Save(ctx, b), ports.ErrConcurrentUpdate)
}

func bookList(t *testing.T, f Fixture) {
	mk := func(node, pickup string, cutoff time.Time) {
		b, err := book.Open(book.Opening{NodeID: node, PickupDepartureID: pickup, CutoffAt: cutoff, HoldDeadlineAt: cutoff.Add(-time.Hour), Share: 3})
		noErr(t, err)
		noErr(t, f.Books.Save(ctx, b))
	}
	day := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	mk("N-AUS3", "dep-b", day.Add(18*time.Hour))
	mk("N-AUS3", "dep-a", day.Add(11*time.Hour))
	mk("N-AUS3", "dep-next", day.Add(35*time.Hour))
	mk("N-DFW7", "dep-c", day.Add(16*time.Hour))
	got, err := f.Books.List(ctx, repository.BookFilter{NodeIDs: []string{"N-AUS3"}, CutoffFrom: day, CutoffUntil: day.Add(24 * time.Hour)})
	noErr(t, err)
	eq(t, "AUS3 on the day", len(got), 2)
	eq(t, "ordered by cutoff", got[0].PickupDepartureID(), "dep-a")
	got, _ = f.Books.List(ctx, repository.BookFilter{NodeIDs: []string{"N-AUS3", "N-DFW7"}, CutoffFrom: day.Add(11 * time.Hour), CutoffUntil: day.Add(18 * time.Hour)})
	eq(t, "from inclusive, until exclusive", len(got), 2)
	got, _ = f.Books.List(ctx, repository.BookFilter{})
	eq(t, "all books", len(got), 4)
	got, _ = f.Books.List(ctx, repository.BookFilter{CutoffFrom: day.Add(36 * time.Hour)})
	eq(t, "none", len(got), 0)
}

func bookOpenDue(t *testing.T, f Fixture) {
	noErr(t, f.Books.Save(ctx, NewBook(t, "N-AUS3", "dep-1", 3)))
	noErr(t, f.Books.Save(ctx, NewBook(t, "N-AUS3", "dep-2", 3)))
	noErr(t, f.Books.Save(ctx, NewBook(t, "N-AUS3", "dep-3", 3)))
	closed, err := f.Books.Get(ctx, "N-AUS3:dep-3")
	noErr(t, err)
	noErr(t, closed.Close(closed.CutoffAt()))
	noErr(t, f.Books.Save(ctx, closed))
	due, err := f.Books.ListOpenDue(ctx, T0.Add(4*time.Hour), 10)
	noErr(t, err)
	eq(t, "open and due", len(due), 2)
	due, _ = f.Books.ListOpenDue(ctx, T0.Add(4*time.Hour-time.Nanosecond), 10)
	eq(t, "none before the cutoff", len(due), 0)
	due, _ = f.Books.ListOpenDue(ctx, T0.Add(5*time.Hour), 1)
	eq(t, "limit", len(due), 1)
}

var policyCases = []named{
	{"Current is ErrNotFound before anything is published", policyEmpty},
	{"the highest version is current and a version round-trips", policyVersions},
	{"a version cannot be stored twice", policyDuplicate},
}

func policyEmpty(t *testing.T, f Fixture) {
	_, err := f.Policies.Current(ctx)
	wantErr(t, err, ports.ErrNotFound)
}

func policyVersions(t *testing.T, f Fixture) {
	noErr(t, f.Policies.Save(ctx, NewPolicy(t, 0)))
	noErr(t, f.Policies.Save(ctx, NewPolicy(t, 1)))
	cur, err := f.Policies.Current(ctx)
	noErr(t, err)
	eq(t, "version", cur.Version(), 2)
	eq(t, "packages", cur.PackagesPerOrder(), 2)
	eq(t, "share", cur.SharePercent(), 80)
	eq(t, "offer ttl", cur.OfferTTL(), 10*time.Minute)
	eq(t, "hold ttl", cur.HoldTTL(), 15*time.Minute)
	eqTime(t, "publishedAt", cur.PublishedAt(), T0)
	ds := cur.Deadlines()
	eq(t, "deadlines", len(ds), 1)
	eq(t, "deadline tier", ds[0].Tier, shared.TierNextDay)
	eq(t, "deadline region", ds[0].RegionID, "R-SC")
	eq(t, "deadline minute", ds[0].Minute, 23*60+59)
}

func policyDuplicate(t *testing.T, f Fixture) {
	noErr(t, f.Policies.Save(ctx, NewPolicy(t, 0)))
	wantErr(t, f.Policies.Save(ctx, NewPolicy(t, 0)), ports.ErrConcurrentUpdate)
}
