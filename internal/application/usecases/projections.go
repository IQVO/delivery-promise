package usecases

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/search"
)

// ScorecardWindow is the only scorecard window the promise reads (E18).
const ScorecardWindow = "30d"

// consume runs fn in a unit of work that first records the CloudEvents id in
// the inbox: a message already processed changes nothing (idempotent consumer).
// fn returns the promises that were flagged AT_RISK and still need a revision;
// they are re-promised after the commit.
func (s *Service) consume(ctx context.Context, m Meta, fn func(ctx context.Context) ([]promise.ID, error)) error {
	var pending []promise.ID
	err := s.tx(ctx, func(ctx context.Context) error {
		pending = nil
		first, err := s.Inbox.Begin(ctx, m.ID, m.Type, s.Clock.Now())
		if err != nil || !first {
			return err
		}
		pending, err = fn(ctx)
		return err
	})
	if err != nil {
		return err
	}
	s.ReviseAll(ctx, pending)
	return nil
}

// project adapts a plain projection (no risk) to consume.
func (s *Service) project(ctx context.Context, m Meta, fn func(ctx context.Context) error) error {
	return s.consume(ctx, m, func(ctx context.Context) ([]promise.ID, error) { return nil, fn(ctx) })
}

func notFound(err error) bool { return errors.Is(err, ports.ErrNotFound) }

// ---- node-topology -----------------------------------------------------------------------------------------

// OnNodeRegistered projects NodeRegistered into NodeProfileView.
func (s *Service) OnNodeRegistered(ctx context.Context, m Meta, e NodeRegistered) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		n, err := s.Network.GetNode(ctx, e.NodeID)
		if err != nil && !notFound(err) {
			return err
		}
		if notFound(err) {
			n = readmodel.Node{ID: e.NodeID, State: readmodel.NodeProposed}
		}
		n.Type, n.Subtype, n.Operator, n.RegionID, n.Timezone = e.NodeType, e.Subtype, e.Operator, e.RegionID, e.Timezone
		return s.Network.PutNode(ctx, n)
	})
}

// OnNodeProfileDeclared projects NodeOperationalProfileDeclared; a revision not
// above the stored one is ignored.
func (s *Service) OnNodeProfileDeclared(ctx context.Context, m Meta, e NodeProfileDeclared) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		n, err := s.Network.GetNode(ctx, e.NodeID)
		if err != nil && !notFound(err) {
			return err
		}
		if notFound(err) {
			n = readmodel.Node{ID: e.NodeID, State: readmodel.NodeProposed, Timezone: e.Timezone}
		}
		if n.Profile != nil && e.ProfileRevision <= n.Profile.Revision {
			return nil
		}
		hours := make(map[string]readmodel.Hours, len(e.OperatingHoursByDay))
		for day, h := range e.OperatingHoursByDay {
			hours[strings.ToLower(day)] = readmodel.Hours{Start: h.Start, End: h.End}
		}
		cutoffs := make([]readmodel.Cutoff, len(e.Cutoffs))
		for i, c := range e.Cutoffs {
			cutoffs[i] = readmodel.Cutoff{LocalTime: c.LocalTime, Days: slices.Clone(c.Days), MaxOrders: c.MaxOrders}
		}
		n.Profile = &readmodel.Profile{
			Revision: e.ProfileRevision, Timezone: e.Timezone, OperatingHours: hours, Holidays: slices.Clone(e.Holidays),
			HandlingMinutes: e.HandlingTimeMinutes, Cutoffs: cutoffs, CapValue: e.ThroughputCap.Value,
			CapPerHour: e.ThroughputCap.TimeUnit == "HOUR", SortDwellMinutes: e.SortDwellMinutes,
		}
		if e.Timezone != "" {
			n.Timezone = e.Timezone
		}
		return s.Network.PutNode(ctx, n)
	})
}

// OnNodeStateChanged projects NodeActivated, NodeSuspended and NodeRetired; state
// is one of readmodel.NodeActive, NodeSuspended or NodeRetired.
func (s *Service) OnNodeStateChanged(ctx context.Context, m Meta, e NodeStateChanged, state string) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		n, err := s.Network.GetNode(ctx, e.NodeID)
		if err != nil && !notFound(err) {
			return err
		}
		if notFound(err) {
			n = readmodel.Node{ID: e.NodeID}
		}
		n.State = state
		return s.Network.PutNode(ctx, n)
	})
}

// OnLaneOpened projects LaneOpened into LaneView.
func (s *Service) OnLaneOpened(ctx context.Context, m Meta, e LaneOpened) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		return s.Network.PutLane(ctx, search.Lane{ID: e.LaneID, Origin: e.OriginNodeID, Dest: e.DestinationNodeID, NominalMinutes: e.NominalTransitMinutes, Open: true})
	})
}

// OnLaneClosed projects LaneClosed.
func (s *Service) OnLaneClosed(ctx context.Context, m Meta, e LaneClosed) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		l, err := s.Network.GetLane(ctx, e.LaneID)
		if err != nil && !notFound(err) {
			return err
		}
		if notFound(err) {
			l = search.Lane{ID: e.LaneID}
		}
		l.Open = false
		return s.Network.PutLane(ctx, l)
	})
}

// OnServiceAreaAssigned projects ServiceAreaAssigned; an assignmentVersion not
// above the stored one is ignored.
func (s *Service) OnServiceAreaAssigned(ctx context.Context, m Meta, e ServiceAreaAssigned) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		cur, err := s.Network.GetArea(ctx, e.PostalPrefix)
		if err != nil && !notFound(err) {
			return err
		}
		if err == nil && e.AssignmentVersion <= cur.Version {
			return nil
		}
		return s.Network.PutArea(ctx, readmodel.Area{PostalPrefix: e.PostalPrefix, StationID: e.DeliveryStationNodeID, RegionID: e.RegionID, Version: e.AssignmentVersion})
	})
}

// OnRegionDefined projects RegionDefined.
func (s *Service) OnRegionDefined(ctx context.Context, m Meta, e RegionDefined) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		return s.Network.PutRegion(ctx, readmodel.Region{ID: e.RegionID, Code: e.Code, Name: e.Name})
	})
}

// ---- offer-availability ------------------------------------------------------------------------------------

// OnCatalogItemChanged projects CatalogItemCreated (discontinued false) and
// CatalogItemDiscontinued (true).
func (s *Service) OnCatalogItemChanged(ctx context.Context, m Meta, e CatalogItemChanged, discontinued bool) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		return s.Network.PutItem(ctx, readmodel.Item{ID: e.CatalogItemID, Discontinued: discontinued})
	})
}

// OnListingCreated projects ListingCreated.
func (s *Service) OnListingCreated(ctx context.Context, m Meta, e ListingCreated) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		if _, err := s.Network.GetListing(ctx, e.ListingID); err == nil {
			return nil
		} else if !notFound(err) {
			return err
		}
		return s.Network.PutListing(ctx, readmodel.Listing{ID: e.ListingID, ItemID: e.CatalogItemID, NodeID: e.NodeID})
	})
}

// OnListingAvailabilityChanged projects ListingAvailabilityChanged; an asOf
// older than the stored one is ignored.
func (s *Service) OnListingAvailabilityChanged(ctx context.Context, m Meta, e ListingAvailabilityChanged) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		l, err := s.Network.GetListing(ctx, e.ListingID)
		if err != nil && !notFound(err) {
			return err
		}
		if err == nil && e.AsOf.Before(l.AsOf) {
			return nil
		}
		l.ID, l.ItemID, l.NodeID, l.AvailableQty, l.AsOf = e.ListingID, e.CatalogItemID, e.NodeID, e.AvailableQty, e.AsOf.UTC()
		return s.Network.PutListing(ctx, l)
	})
}

// OnListingToggled projects ListingSuppressed (suppressed true) and
// ListingRestored (false).
func (s *Service) OnListingToggled(ctx context.Context, m Meta, e ListingToggled, suppressed bool) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		l, err := s.Network.GetListing(ctx, e.ListingID)
		if err != nil && !notFound(err) {
			return err
		}
		if notFound(err) {
			l = readmodel.Listing{ID: e.ListingID, NodeID: e.NodeID}
		}
		l.Suppressed = suppressed
		return s.Network.PutListing(ctx, l)
	})
}

// ---- node-performance --------------------------------------------------------------------------------------

// OnScorecardUpdated projects NodeScorecardUpdated; only the 30d window counts
// (E18).
func (s *Service) OnScorecardUpdated(ctx context.Context, m Meta, e ScorecardUpdated) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		if e.Window != ScorecardWindow {
			return nil
		}
		return s.Network.PutScorecard(ctx, readmodel.Scorecard{NodeID: e.NodeID, Grade: e.Grade, Observations: e.Observations, ComputedAt: e.ComputedAt.UTC()})
	})
}
