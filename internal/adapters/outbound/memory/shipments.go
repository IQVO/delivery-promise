package memory

import (
	"cmp"
	"context"
	"slices"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
)

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

// PutShipment records the tracking numbers of an assignment; a number another
// assignment already owns is shared, not re-owned (ADR 0025).
func (v *ShipmentViews) PutShipment(ctx context.Context, assignmentID string, trackingNumbers []string) error {
	defer v.s.enter(ctx)()
	v.s.st.shipments[assignmentID] = slices.Clone(trackingNumbers)
	for _, t := range trackingNumbers {
		owners := v.s.st.trackingOf[t]
		if slices.Contains(owners, assignmentID) {
			continue
		}
		// a fresh slice: the transactional snapshot clones the map, not the slices
		owners = append(slices.Clone(owners), assignmentID)
		slices.Sort(owners)
		v.s.st.trackingOf[t] = owners
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

// AssignmentsOf returns every assignment a tracking number was shipped under,
// sorted ascending, or ports.ErrNotFound when there is none.
func (v *ShipmentViews) AssignmentsOf(ctx context.Context, trackingNumber string) ([]string, error) {
	defer v.s.enter(ctx)()
	owners := v.s.st.trackingOf[trackingNumber]
	if len(owners) == 0 {
		return nil, ports.ErrNotFound
	}
	return slices.Clone(owners), nil
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
