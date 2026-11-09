package usecases

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/repository"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// ReservationLag is how long after a promise is confirmed the warehouse is
// taken to have reserved its units, so that a listing figure computed from
// then on already excludes them (ADR 0015). It comes from the docs' worked
// example (order-assignment-model.md): order placed 14:10, held order raised
// 15:56, released to the floor 16:02, i.e. 1h52 later; 2h covers it.
const ReservationLag = 2 * time.Hour

// keyOf is the AvailabilityView key of a listing: (node, item, ownerRef), ""
// for an operator-owned listing (ADR 0017).
func keyOf(l readmodel.Listing) repository.HoldKey {
	return repository.HoldKey{NodeID: l.NodeID, ItemID: l.ItemID, OwnerRef: l.OwnerRef}
}

// latestAsOf is the newest asOf among the listings of each (node, item,
// ownerRef) key: the instant the figure the promise side sells against was
// computed.
func latestAsOf(listings []readmodel.Listing) map[repository.HoldKey]time.Time {
	out := map[repository.HoldKey]time.Time{}
	for _, l := range listings {
		k := keyOf(l)
		if l.AsOf.After(out[k]) {
			out[k] = l.AsOf
		}
	}
	return out
}

// addUnaccounted adds to holds the units of confirmed promises that the
// listing figure of their own (node, item, ownerRef) key cannot include yet:
// that figure was computed before the warehouse reserved the order
// (confirmedAt + ReservationLag). A figure that lands late or out of order
// carries an older asOf and so releases nothing; another owner's figure never
// releases it (ADR 0015, ADR 0017).
func addUnaccounted(holds map[repository.HoldKey]int, confirmed []repository.ConfirmedHold, asOf map[repository.HoldKey]time.Time) {
	for _, c := range confirmed {
		if figure, ok := asOf[c.Key]; ok && !figure.Before(c.ConfirmedAt.Add(ReservationLag)) {
			continue
		}
		holds[c.Key] += c.Qty
	}
}

// nodeItem indexes the owners of one item at one node.
type nodeItem struct{ node, item string }

// stockIndex is AvailabilityView netted per (node, item, ownerRef) and grouped
// by (node, item): built once per quote, so the search's per-(node, line)
// lookup is one map read, never a scan of the listings.
type stockIndex map[nodeItem][]shared.OwnerStock

func (x stockIndex) of(node, item string) []shared.OwnerStock { return x[nodeItem{node, item}] }

// netStock nets each listing figure by the holds of its own key, never by
// another owner's, and lists the owners of each (node, item) by ownerRef. A
// suppressed listing, one of a discontinued item and a row that knows no item
// yet ship nothing.
func netStock(listings []readmodel.Listing, discontinued map[string]bool, holds map[repository.HoldKey]int) stockIndex {
	qty := map[repository.HoldKey]int{}
	var keys []repository.HoldKey
	for _, l := range listings {
		if l.Suppressed || l.ItemID == "" || discontinued[l.ItemID] {
			continue
		}
		k := keyOf(l)
		if _, seen := qty[k]; !seen {
			keys = append(keys, k)
		}
		qty[k] += l.AvailableQty
	}
	slices.SortFunc(keys, func(a, b repository.HoldKey) int { return cmp.Compare(a.OwnerRef, b.OwnerRef) })
	out := stockIndex{}
	for _, k := range keys {
		ni := nodeItem{k.NodeID, k.ItemID}
		out[ni] = append(out[ni], shared.OwnerStock{OwnerRef: k.OwnerRef, Available: max(0, qty[k]-holds[k])})
	}
	return out
}

// availability is the search's per-owner stock lookup: each owner's listing
// figure, minus the provisional and unaccounted confirmed holds of that same
// owner (ADR 0002 A3, ADR 0015, ADR 0017).
func availability(net readmodel.Network, holds map[repository.HoldKey]int) func(node, item string) []shared.OwnerStock {
	discontinued := map[string]bool{}
	for _, it := range net.Items {
		discontinued[it.ID] = it.Discontinued
	}
	return netStock(net.Listings, discontinued, holds).of
}

// nodeStock returns, per item of the lines, the net units each owner can ship
// from one node: what MakePromise and the lock rehome re-apply the OwnerChoice
// rule to.
func (s *Service) nodeStock(ctx context.Context, nodeID string, lines []shared.Line) (map[string][]shared.OwnerStock, error) {
	items := make([]string, len(lines))
	for i, l := range lines {
		items[i] = l.CatalogItemID
	}
	listings, err := s.Network.ListingsForNode(ctx, nodeID, items)
	if err != nil {
		return nil, err
	}
	holds, err := s.Promises.HeldQuantities(ctx, []string{nodeID})
	if err != nil {
		return nil, err
	}
	confirmed, err := s.Promises.ConfirmedHolds(ctx, []string{nodeID})
	if err != nil {
		return nil, err
	}
	addUnaccounted(holds, confirmed, latestAsOf(listings))
	discontinued, err := s.discontinuedOf(ctx, items)
	if err != nil {
		return nil, err
	}
	idx := netStock(listings, discontinued, holds)
	out := make(map[string][]shared.OwnerStock, len(items))
	for _, item := range items {
		out[item] = idx.of(nodeID, item)
	}
	return out, nil
}

// discontinuedOf reads the catalogue state of the items; an unknown item is
// not discontinued.
func (s *Service) discontinuedOf(ctx context.Context, items []string) (map[string]bool, error) {
	out := make(map[string]bool, len(items))
	for _, id := range items {
		it, err := s.Network.GetItem(ctx, id)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[id] = it.Discontinued
	}
	return out, nil
}
