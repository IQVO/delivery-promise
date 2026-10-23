package memory

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/search"
)

func get[V any](s *Store, ctx context.Context, m map[string]V, key string) (V, error) {
	defer s.enter(ctx)()
	v, ok := m[key]
	if !ok {
		var zero V
		return zero, ports.ErrNotFound
	}
	return v, nil
}

func put[V any](s *Store, ctx context.Context, m map[string]V, key string, v V) error {
	defer s.enter(ctx)()
	m[key] = v
	return nil
}

// NetworkViews is the in-memory ports.NetworkViews.
type NetworkViews struct{ s *Store }

func cloneNode(n readmodel.Node) readmodel.Node {
	if n.Profile != nil {
		p := *n.Profile
		p.Holidays = slices.Clone(p.Holidays)
		p.Cutoffs = slices.Clone(p.Cutoffs)
		hours := make(map[string]readmodel.Hours, len(p.OperatingHours))
		for k, v := range p.OperatingHours {
			hours[k] = v
		}
		p.OperatingHours = hours
		n.Profile = &p
	}
	return n
}

// GetNode returns the node row or ports.ErrNotFound.
func (v *NetworkViews) GetNode(ctx context.Context, id string) (readmodel.Node, error) {
	n, err := get(v.s, ctx, v.s.st.nodes, id)
	return cloneNode(n), err
}

// PutNode stores the node row.
func (v *NetworkViews) PutNode(ctx context.Context, n readmodel.Node) error {
	return put(v.s, ctx, v.s.st.nodes, n.ID, cloneNode(n))
}

// GetRegion returns the region row or ports.ErrNotFound.
func (v *NetworkViews) GetRegion(ctx context.Context, id string) (readmodel.Region, error) {
	return get(v.s, ctx, v.s.st.regions, id)
}

// PutRegion stores the region row.
func (v *NetworkViews) PutRegion(ctx context.Context, r readmodel.Region) error {
	return put(v.s, ctx, v.s.st.regions, r.ID, r)
}

// GetArea returns the service-area row or ports.ErrNotFound.
func (v *NetworkViews) GetArea(ctx context.Context, prefix string) (readmodel.Area, error) {
	return get(v.s, ctx, v.s.st.areas, prefix)
}

// PutArea stores the service-area row.
func (v *NetworkViews) PutArea(ctx context.Context, a readmodel.Area) error {
	return put(v.s, ctx, v.s.st.areas, a.PostalPrefix, a)
}

// GetLane returns the lane row or ports.ErrNotFound.
func (v *NetworkViews) GetLane(ctx context.Context, id string) (search.Lane, error) {
	return get(v.s, ctx, v.s.st.lanes, id)
}

// PutLane stores the lane row.
func (v *NetworkViews) PutLane(ctx context.Context, l search.Lane) error {
	return put(v.s, ctx, v.s.st.lanes, l.ID, l)
}

// GetItem returns the item row or ports.ErrNotFound.
func (v *NetworkViews) GetItem(ctx context.Context, id string) (readmodel.Item, error) {
	return get(v.s, ctx, v.s.st.items, id)
}

// PutItem stores the item row.
func (v *NetworkViews) PutItem(ctx context.Context, i readmodel.Item) error {
	return put(v.s, ctx, v.s.st.items, i.ID, i)
}

// GetListing returns the listing row or ports.ErrNotFound.
func (v *NetworkViews) GetListing(ctx context.Context, id string) (readmodel.Listing, error) {
	return get(v.s, ctx, v.s.st.listings, id)
}

// PutListing stores the listing row.
func (v *NetworkViews) PutListing(ctx context.Context, l readmodel.Listing) error {
	return put(v.s, ctx, v.s.st.listings, l.ID, l)
}

// ListingsForNode returns the node's listings of the items by listing id.
func (v *NetworkViews) ListingsForNode(ctx context.Context, nodeID string, itemIDs []string) ([]readmodel.Listing, error) {
	defer v.s.enter(ctx)()
	var out []readmodel.Listing
	for _, l := range v.s.st.listings {
		if l.NodeID == nodeID && slices.Contains(itemIDs, l.ItemID) {
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b readmodel.Listing) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// GetDeparture returns the departure row or ports.ErrNotFound.
func (v *NetworkViews) GetDeparture(ctx context.Context, id string) (search.Departure, error) {
	return get(v.s, ctx, v.s.st.departures, id)
}

// PutDeparture stores the departure row.
func (v *NetworkViews) PutDeparture(ctx context.Context, d search.Departure) error {
	return put(v.s, ctx, v.s.st.departures, d.ID, d)
}

// GetWave returns the wave row or ports.ErrNotFound.
func (v *NetworkViews) GetWave(ctx context.Context, id string) (search.Wave, error) {
	return get(v.s, ctx, v.s.st.waves, id)
}

// PutWave stores the wave row.
func (v *NetworkViews) PutWave(ctx context.Context, w search.Wave) error {
	return put(v.s, ctx, v.s.st.waves, w.ID, w)
}

// GetScorecard returns the scorecard row or ports.ErrNotFound.
func (v *NetworkViews) GetScorecard(ctx context.Context, nodeID string) (readmodel.Scorecard, error) {
	return get(v.s, ctx, v.s.st.scorecards, nodeID)
}

// PutScorecard stores the scorecard row.
func (v *NetworkViews) PutScorecard(ctx context.Context, sc readmodel.Scorecard) error {
	return put(v.s, ctx, v.s.st.scorecards, sc.NodeID, sc)
}

// Load returns the slice of the network a search needs (see ports.NetworkViews).
func (v *NetworkViews) Load(ctx context.Context, q readmodel.Query) (readmodel.Network, error) {
	defer v.s.enter(ctx)()
	st := v.s.st
	var net readmodel.Network
	nodeIDs := map[string]bool{q.StationID: true}
	for _, l := range st.listings {
		if slices.Contains(q.ItemIDs, l.ItemID) {
			net.Listings = append(net.Listings, l)
			nodeIDs[l.NodeID] = true
		}
	}
	for _, d := range st.departures {
		if d.Scheduled && !d.CutoffAt.Before(q.From) && d.CutoffAt.Before(q.Until) {
			net.Departures = append(net.Departures, d)
			nodeIDs[d.Origin], nodeIDs[d.Dest] = true, true
		}
	}
	for _, w := range st.waves {
		if w.Scheduled && w.Station == q.StationID && !w.WindowEnd.Before(q.From) {
			net.Waves = append(net.Waves, w)
		}
	}
	net.Lanes = valuesOf(st.lanes)
	loadNodes(&net, st, nodeIDs, q.ItemIDs)
	sortNetwork(&net)
	return net, nil
}

func valuesOf[V any](m map[string]V) []V {
	out := make([]V, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// loadNodes adds the node, scorecard and item rows the other rows refer to.
func loadNodes(net *readmodel.Network, st state, nodeIDs map[string]bool, itemIDs []string) {
	for id := range nodeIDs {
		if n, ok := st.nodes[id]; ok {
			net.Nodes = append(net.Nodes, cloneNode(n))
		}
		if sc, ok := st.scorecards[id]; ok {
			net.Scorecards = append(net.Scorecards, sc)
		}
	}
	for _, id := range itemIDs {
		if it, ok := st.items[id]; ok {
			net.Items = append(net.Items, it)
		}
	}
}

func sortNetwork(n *readmodel.Network) {
	slices.SortFunc(n.Nodes, func(a, b readmodel.Node) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(n.Listings, func(a, b readmodel.Listing) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(n.Items, func(a, b readmodel.Item) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(n.Scorecards, func(a, b readmodel.Scorecard) int { return cmp.Compare(a.NodeID, b.NodeID) })
	slices.SortFunc(n.Lanes, func(a, b search.Lane) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(n.Departures, func(a, b search.Departure) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(n.Waves, func(a, b search.Wave) int { return cmp.Compare(a.ID, b.ID) })
}

// ShipmentViews is the in-memory ports.ShipmentViews.
type ShipmentViews struct{ s *Store }

func cloneAssignment(a readmodel.Assignment) readmodel.Assignment {
	a.ExcludedNodes, a.ExcludedDepartures = slices.Clone(a.ExcludedNodes), slices.Clone(a.ExcludedDepartures)
	return a
}

// GetAssignment returns the assignment or ports.ErrNotFound.
func (v *ShipmentViews) GetAssignment(ctx context.Context, id string) (readmodel.Assignment, error) {
	a, err := get(v.s, ctx, v.s.st.assignments, id)
	return cloneAssignment(a), err
}

// PutAssignment stores the assignment.
func (v *ShipmentViews) PutAssignment(ctx context.Context, a readmodel.Assignment) error {
	return put(v.s, ctx, v.s.st.assignments, a.ID, cloneAssignment(a))
}

// FindAssignmentByPromise returns the assignment of the promise.
func (v *ShipmentViews) FindAssignmentByPromise(ctx context.Context, promiseID string) (readmodel.Assignment, error) {
	defer v.s.enter(ctx)()
	var found []readmodel.Assignment
	for _, a := range v.s.st.assignments {
		if a.PromiseID == promiseID {
			found = append(found, a)
		}
	}
	if len(found) == 0 {
		return readmodel.Assignment{}, ports.ErrNotFound
	}
	slices.SortFunc(found, func(a, b readmodel.Assignment) int { return cmp.Compare(a.ID, b.ID) })
	return cloneAssignment(found[0]), nil
}

// PutShipment records the tracking numbers of an assignment.
func (v *ShipmentViews) PutShipment(ctx context.Context, assignmentID string, trackingNumbers []string) error {
	defer v.s.enter(ctx)()
	v.s.st.shipments[assignmentID] = slices.Clone(trackingNumbers)
	for _, t := range trackingNumbers {
		v.s.st.trackingOf[t] = assignmentID
	}
	return nil
}

// Shipment returns the assignment's tracking numbers.
func (v *ShipmentViews) Shipment(ctx context.Context, assignmentID string) ([]string, error) {
	defer v.s.enter(ctx)()
	out := slices.Clone(v.s.st.shipments[assignmentID])
	slices.Sort(out)
	return out, nil
}

// AssignmentOf returns the assignment a tracking number was shipped under.
func (v *ShipmentViews) AssignmentOf(ctx context.Context, trackingNumber string) (string, error) {
	return get(v.s, ctx, v.s.st.trackingOf, trackingNumber)
}

// PutOutcome records the first final fact of a package.
func (v *ShipmentViews) PutOutcome(ctx context.Context, o readmodel.PackageOutcome) error {
	defer v.s.enter(ctx)()
	if _, seen := v.s.st.outcomes[o.TrackingNumber]; !seen {
		v.s.st.outcomes[o.TrackingNumber] = o
	}
	return nil
}

// Outcomes returns the recorded outcomes of the tracking numbers.
func (v *ShipmentViews) Outcomes(ctx context.Context, trackingNumbers []string) ([]readmodel.PackageOutcome, error) {
	defer v.s.enter(ctx)()
	var out []readmodel.PackageOutcome
	for _, t := range trackingNumbers {
		if o, ok := v.s.st.outcomes[t]; ok {
			out = append(out, o)
		}
	}
	slices.SortFunc(out, func(a, b readmodel.PackageOutcome) int { return cmp.Compare(a.TrackingNumber, b.TrackingNumber) })
	return out, nil
}

// FakeClock is a settable clock for tests.
type FakeClock struct {
	now time.Time
}

// NewFakeClock returns a clock fixed at t.
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{now: t.UTC()} }

// Now returns the fixed instant.
func (c *FakeClock) Now() time.Time { return c.now }

// Set moves the clock.
func (c *FakeClock) Set(t time.Time) { c.now = t.UTC() }

// Advance moves the clock forward by d.
func (c *FakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// SeqIDs mints deterministic ids off-1, off-2 ... and prm-1, prm-2 ...
type SeqIDs struct {
	offers, promises int
}

// NewOfferID returns the next offer id.
func (g *SeqIDs) NewOfferID() offer.ID {
	g.offers++
	return offer.ID("off-" + strconv.Itoa(g.offers))
}

// NewPromiseID returns the next promise id.
func (g *SeqIDs) NewPromiseID() promise.ID {
	g.promises++
	return promise.ID("prm-" + strconv.Itoa(g.promises))
}
