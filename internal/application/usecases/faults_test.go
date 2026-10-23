package usecases_test

// Code generated for fault injection; edit the generator in the PR that changed ports, not this file by hand.

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/repository"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

var (
	_ = time.Now
	_ = repository.BookFilter{}
	_ = readmodel.Node{}
	_ = search.Lane{}
	_ = shared.Event(nil)
	_ = book.ID("")
	_ = offer.ID("")
	_ = policy.Input{}
	_ = promise.ID("")
)

// errInjected is the failure a faults value injects.
var errInjected = errors.New("injected failure")

// faults fails the failAt-th port call (1-based; 0 never) and counts the calls.
type faults struct {
	mu     sync.Mutex
	failAt int
	calls  int
}

func (f *faults) hit() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == f.failAt {
		return errInjected
	}
	return nil
}

type fOfferRepository struct {
	in ports.OfferRepository
	f  *faults
}

func (fw *fOfferRepository) Get(ctx context.Context, id offer.ID) (*offer.Offer, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.Get(ctx, id)
}

func (fw *fOfferRepository) ListExpired(ctx context.Context, now time.Time, limit int) ([]offer.ID, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.ListExpired(ctx, now, limit)
}

func (fw *fOfferRepository) Save(ctx context.Context, o *offer.Offer) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.Save(ctx, o)
}

type fPromiseRepository struct {
	in ports.PromiseRepository
	f  *faults
}

func (fw *fPromiseRepository) Get(ctx context.Context, id promise.ID) (*promise.Promise, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.Get(ctx, id)
}

func (fw *fPromiseRepository) GetByOffer(ctx context.Context, offerID string) (*promise.Promise, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.GetByOffer(ctx, offerID)
}

func (fw *fPromiseRepository) FindByOrderGroup(ctx context.Context, orderID string, groupNo int) (*promise.Promise, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.FindByOrderGroup(ctx, orderID, groupNo)
}

func (fw *fPromiseRepository) ListByOrder(ctx context.Context, orderID string) ([]*promise.Promise, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.ListByOrder(ctx, orderID)
}

func (fw *fPromiseRepository) ListHoldsExpired(ctx context.Context, now time.Time, limit int) ([]promise.ID, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.ListHoldsExpired(ctx, now, limit)
}

func (fw *fPromiseRepository) ListAtRisk(ctx context.Context, limit int) ([]promise.ID, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.ListAtRisk(ctx, limit)
}

func (fw *fPromiseRepository) ListActiveByPath(ctx context.Context, f repository.PromiseRiskFilter) ([]promise.ID, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.ListActiveByPath(ctx, f)
}

func (fw *fPromiseRepository) HeldQuantities(ctx context.Context, nodeIDs []string) (map[repository.HoldKey]int, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.HeldQuantities(ctx, nodeIDs)
}

func (fw *fPromiseRepository) Save(ctx context.Context, p *promise.Promise) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.Save(ctx, p)
}

type fBookRepository struct {
	in ports.BookRepository
	f  *faults
}

func (fw *fBookRepository) Get(ctx context.Context, id book.ID) (*book.Book, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.Get(ctx, id)
}

func (fw *fBookRepository) List(ctx context.Context, f repository.BookFilter) ([]*book.Book, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.List(ctx, f)
}

func (fw *fBookRepository) ListOpenDue(ctx context.Context, now time.Time, limit int) ([]book.ID, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.ListOpenDue(ctx, now, limit)
}

func (fw *fBookRepository) Save(ctx context.Context, b *book.Book) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.Save(ctx, b)
}

type fPolicyRepository struct {
	in ports.PolicyRepository
	f  *faults
}

func (fw *fPolicyRepository) Current(ctx context.Context) (*policy.Policy, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.Current(ctx)
}

func (fw *fPolicyRepository) Save(ctx context.Context, p *policy.Policy) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.Save(ctx, p)
}

type fNetworkViews struct {
	in ports.NetworkViews
	f  *faults
}

func (fw *fNetworkViews) GetNode(ctx context.Context, id string) (readmodel.Node, error) {
	if err := fw.f.hit(); err != nil {
		return readmodel.Node{}, err
	}
	return fw.in.GetNode(ctx, id)
}

func (fw *fNetworkViews) PutNode(ctx context.Context, n readmodel.Node) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.PutNode(ctx, n)
}

func (fw *fNetworkViews) GetRegion(ctx context.Context, id string) (readmodel.Region, error) {
	if err := fw.f.hit(); err != nil {
		return readmodel.Region{}, err
	}
	return fw.in.GetRegion(ctx, id)
}

func (fw *fNetworkViews) PutRegion(ctx context.Context, r readmodel.Region) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.PutRegion(ctx, r)
}

func (fw *fNetworkViews) GetArea(ctx context.Context, postalPrefix string) (readmodel.Area, error) {
	if err := fw.f.hit(); err != nil {
		return readmodel.Area{}, err
	}
	return fw.in.GetArea(ctx, postalPrefix)
}

func (fw *fNetworkViews) PutArea(ctx context.Context, a readmodel.Area) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.PutArea(ctx, a)
}

func (fw *fNetworkViews) GetLane(ctx context.Context, id string) (search.Lane, error) {
	if err := fw.f.hit(); err != nil {
		return search.Lane{}, err
	}
	return fw.in.GetLane(ctx, id)
}

func (fw *fNetworkViews) PutLane(ctx context.Context, l search.Lane) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.PutLane(ctx, l)
}

func (fw *fNetworkViews) GetItem(ctx context.Context, id string) (readmodel.Item, error) {
	if err := fw.f.hit(); err != nil {
		return readmodel.Item{}, err
	}
	return fw.in.GetItem(ctx, id)
}

func (fw *fNetworkViews) PutItem(ctx context.Context, i readmodel.Item) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.PutItem(ctx, i)
}

func (fw *fNetworkViews) GetListing(ctx context.Context, id string) (readmodel.Listing, error) {
	if err := fw.f.hit(); err != nil {
		return readmodel.Listing{}, err
	}
	return fw.in.GetListing(ctx, id)
}

func (fw *fNetworkViews) ListingsForNode(ctx context.Context, nodeID string, itemIDs []string) ([]readmodel.Listing, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.ListingsForNode(ctx, nodeID, itemIDs)
}

func (fw *fNetworkViews) PutListing(ctx context.Context, l readmodel.Listing) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.PutListing(ctx, l)
}

func (fw *fNetworkViews) GetDeparture(ctx context.Context, id string) (search.Departure, error) {
	if err := fw.f.hit(); err != nil {
		return search.Departure{}, err
	}
	return fw.in.GetDeparture(ctx, id)
}

func (fw *fNetworkViews) PutDeparture(ctx context.Context, d search.Departure) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.PutDeparture(ctx, d)
}

func (fw *fNetworkViews) GetWave(ctx context.Context, id string) (search.Wave, error) {
	if err := fw.f.hit(); err != nil {
		return search.Wave{}, err
	}
	return fw.in.GetWave(ctx, id)
}

func (fw *fNetworkViews) PutWave(ctx context.Context, w search.Wave) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.PutWave(ctx, w)
}

func (fw *fNetworkViews) GetScorecard(ctx context.Context, nodeID string) (readmodel.Scorecard, error) {
	if err := fw.f.hit(); err != nil {
		return readmodel.Scorecard{}, err
	}
	return fw.in.GetScorecard(ctx, nodeID)
}

func (fw *fNetworkViews) PutScorecard(ctx context.Context, s readmodel.Scorecard) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.PutScorecard(ctx, s)
}

func (fw *fNetworkViews) Load(ctx context.Context, q readmodel.Query) (readmodel.Network, error) {
	if err := fw.f.hit(); err != nil {
		return readmodel.Network{}, err
	}
	return fw.in.Load(ctx, q)
}

type fShipmentViews struct {
	in ports.ShipmentViews
	f  *faults
}

func (fw *fShipmentViews) GetAssignment(ctx context.Context, assignmentID string) (readmodel.Assignment, error) {
	if err := fw.f.hit(); err != nil {
		return readmodel.Assignment{}, err
	}
	return fw.in.GetAssignment(ctx, assignmentID)
}

func (fw *fShipmentViews) PutAssignment(ctx context.Context, a readmodel.Assignment) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.PutAssignment(ctx, a)
}

func (fw *fShipmentViews) FindAssignmentByPromise(ctx context.Context, promiseID string) (readmodel.Assignment, error) {
	if err := fw.f.hit(); err != nil {
		return readmodel.Assignment{}, err
	}
	return fw.in.FindAssignmentByPromise(ctx, promiseID)
}

func (fw *fShipmentViews) PutShipment(ctx context.Context, assignmentID string, trackingNumbers []string) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.PutShipment(ctx, assignmentID, trackingNumbers)
}

func (fw *fShipmentViews) Shipment(ctx context.Context, assignmentID string) ([]string, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.Shipment(ctx, assignmentID)
}

func (fw *fShipmentViews) AssignmentOf(ctx context.Context, trackingNumber string) (string, error) {
	if err := fw.f.hit(); err != nil {
		return "", err
	}
	return fw.in.AssignmentOf(ctx, trackingNumber)
}

func (fw *fShipmentViews) PutOutcome(ctx context.Context, o readmodel.PackageOutcome) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.PutOutcome(ctx, o)
}

func (fw *fShipmentViews) Outcomes(ctx context.Context, trackingNumbers []string) ([]readmodel.PackageOutcome, error) {
	if err := fw.f.hit(); err != nil {
		return nil, err
	}
	return fw.in.Outcomes(ctx, trackingNumbers)
}

type fInbox struct {
	in ports.Inbox
	f  *faults
}

func (fw *fInbox) Begin(ctx context.Context, eventID, eventType string, at time.Time) (bool, error) {
	if err := fw.f.hit(); err != nil {
		return false, err
	}
	return fw.in.Begin(ctx, eventID, eventType, at)
}

type fEventOutbox struct {
	in ports.EventOutbox
	f  *faults
}

func (fw *fEventOutbox) Append(ctx context.Context, events ...shared.Event) error {
	if err := fw.f.hit(); err != nil {
		return err
	}
	return fw.in.Append(ctx, events...)
}
