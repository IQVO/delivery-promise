package usecases_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/repository"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// calls counts every port call an estimate could make, read or write.
type calls map[string]int

type countingNetwork struct {
	ports.NetworkViews
	c calls
}

func (n countingNetwork) GetArea(ctx context.Context, prefix string) (readmodel.Area, error) {
	n.c["Network.GetArea"]++
	return n.NetworkViews.GetArea(ctx, prefix)
}

func (n countingNetwork) GetNode(ctx context.Context, id string) (readmodel.Node, error) {
	n.c["Network.GetNode"]++
	return n.NetworkViews.GetNode(ctx, id)
}

func (n countingNetwork) GetItem(ctx context.Context, id string) (readmodel.Item, error) {
	n.c["Network.GetItem"]++
	return n.NetworkViews.GetItem(ctx, id)
}

func (n countingNetwork) ListingsForNode(ctx context.Context, node string, items []string) ([]readmodel.Listing, error) {
	n.c["Network.ListingsForNode"]++
	return n.NetworkViews.ListingsForNode(ctx, node, items)
}

func (n countingNetwork) Load(ctx context.Context, q readmodel.Query) (readmodel.Network, error) {
	n.c["Network.Load"]++
	return n.NetworkViews.Load(ctx, q)
}

type countingPromises struct {
	ports.PromiseRepository
	c calls
}

func (p countingPromises) HeldQuantities(ctx context.Context, nodes []string) (map[repository.HoldKey]int, error) {
	p.c["Promises.HeldQuantities"]++
	return p.PromiseRepository.HeldQuantities(ctx, nodes)
}

func (p countingPromises) ConfirmedHolds(ctx context.Context, nodes []string) ([]repository.ConfirmedHold, error) {
	p.c["Promises.ConfirmedHolds"]++
	return p.PromiseRepository.ConfirmedHolds(ctx, nodes)
}

type countingBooks struct {
	ports.BookRepository
	c calls
}

func (b countingBooks) List(ctx context.Context, f repository.BookFilter) ([]*book.Book, error) {
	b.c["Books.List"]++
	return b.BookRepository.List(ctx, f)
}

type countingPolicies struct {
	ports.PolicyRepository
	c calls
}

func (p countingPolicies) Current(ctx context.Context) (*policy.Policy, error) {
	p.c["Policies.Current"]++
	return p.PolicyRepository.Current(ctx)
}

type countingTransit struct {
	ports.TransitActuals
	c calls
}

func (tr countingTransit) MeasuredTransit(ctx context.Context, origin, dest string, percentile, minObs, window int, now time.Time) (bool, int) {
	tr.c["TransitActuals.MeasuredTransit"]++
	return tr.TransitActuals.MeasuredTransit(ctx, origin, dest, percentile, minObs, window, now)
}

// The write seams: an estimate must never reach any of them.
type countingWrites struct {
	c calls
}

func (w countingWrites) Do(ctx context.Context, fn func(context.Context) error) error {
	w.c["UnitOfWork.Do"]++
	return fn(ctx)
}

func (w countingWrites) Append(context.Context, ...shared.Event) error {
	w.c["Outbox.Append"]++
	return nil
}

func (w countingWrites) NewOfferID() offer.ID {
	w.c["IDs.NewOfferID"]++
	return "off-x"
}

func (w countingWrites) NewPromiseID() promise.ID {
	w.c["IDs.NewPromiseID"]++
	return "prm-x"
}

// countingService is a Service over the env's store whose every port counts
// its calls into c.
func (e *env) countingService(c calls) *usecases.Service {
	w := countingWrites{c}
	return usecases.New(usecases.Deps{
		UoW: w, Offers: countingOffers{e.store.Offers(), c}, Promises: countingPromises{e.store.Promises(), c},
		Books: countingBooks{e.store.Books(), c}, Policies: countingPolicies{e.store.Policies(), c},
		Network: countingNetwork{e.store.Network(), c}, Shipments: e.store.Shipments(),
		TransitActuals: countingTransit{e.store.TransitActuals(), c}, Outbox: w, Inbox: e.store.Inbox(), Clock: e.clock, IDs: w,
	})
}

type countingOffers struct {
	ports.OfferRepository
	c calls
}

func (o countingOffers) Save(ctx context.Context, of *offer.Offer) error {
	o.c["Offers.Save"]++
	return o.OfferRepository.Save(ctx, of)
}

// fiftyItems stocks ci-p00..ci-p49 at the worked example's node and returns
// one line of each.
func (e *env) fiftyItems() []shared.Line {
	items := make([]shared.Line, usecases.MaxEstimateItems)
	for i := range items {
		id := fmt.Sprintf("ci-p%02d", i)
		e.stock("N-AUS3", id, "lst-p"+id, 10)
		items[i] = shared.Line{CatalogItemID: id, Quantity: 1}
	}
	return items
}

// The performance guard of ADR 0019: a 50-item estimate makes exactly the
// read-model loads of a 1-item estimate (one of each shared input), at most
// one measured-transit read per lane pair, and no write call at all.
func TestAFiftyItemEstimateLoadsTheSharedInputsOnce(t *testing.T) {
	e := newEnv(t)
	for i := range 20 { // a measured lane, so the transit lookup is exercised
		e.loadObserved("ld-p-"+string(rune('a'+i)), "N-SAU1", "N-DAU1", 135, at(21, 20, 0).Add(time.Duration(i)*time.Hour))
	}
	items := e.fiftyItems()
	one, fifty := calls{}, calls{}
	if _, err := e.countingService(one).EstimateDeliveries(ctx, usecases.EstimateCommand{DestinationPostalPrefix: "787", Items: items[:1]}); err != nil {
		t.Fatal(err)
	}
	res, err := e.countingService(fifty).EstimateDeliveries(ctx, usecases.EstimateCommand{DestinationPostalPrefix: "787", Items: items})
	must(t, err)
	want := calls{
		"Policies.Current": 1, "Network.GetArea": 1, "Network.GetNode": 1, "Network.Load": 1,
		"Promises.HeldQuantities": 1, "Promises.ConfirmedHolds": 1, "Books.List": 1,
	}
	transit := fifty["TransitActuals.MeasuredTransit"]
	delete(fifty, "TransitActuals.MeasuredTransit")
	if fmt.Sprint(fifty) != fmt.Sprint(want) {
		t.Fatalf("50-item estimate port calls = %v, want exactly %v (no write, no per-item load)", fifty, want)
	}
	if transit < 1 || transit != one["TransitActuals.MeasuredTransit"] {
		t.Fatalf("measured-transit reads: 50 items %d, 1 item %d, want equal and at least one", transit, one["TransitActuals.MeasuredTransit"])
	}
	eq(t, "estimates", len(res.Estimates), usecases.MaxEstimateItems)
	for _, est := range res.Estimates {
		eq(t, est.CatalogItemID+" NEXT_DAY basis", optionOf(t, est, "NEXT_DAY").Basis, string(shared.BasisMeasured))
	}
}
