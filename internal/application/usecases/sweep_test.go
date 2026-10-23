package usecases_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/memory"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// faulty is the Service of e with every repository, view, outbox and inbox call
// routed through f; the unit of work stays the store's, so a failed call must
// leave no trace.
func (e *env) faulty(f *faults) *usecases.Service {
	s := e.store
	return usecases.New(usecases.Deps{
		UoW: s.UnitOfWork(), Offers: &fOfferRepository{s.Offers(), f}, Promises: &fPromiseRepository{s.Promises(), f},
		Books: &fBookRepository{s.Books(), f}, Policies: &fPolicyRepository{s.Policies(), f}, Network: &fNetworkViews{s.Network(), f},
		Shipments: &fShipmentViews{s.Shipments(), f}, Outbox: &fEventOutbox{s.Outbox(), f}, Inbox: &fInbox{s.Inbox(), f},
		Clock: e.clock, IDs: e.ids,
	})
}

// scenario prepares an env with the clean Service and returns the one call to
// make with the faulty Service.
type scenario struct {
	name string
	// bestEffort marks calls that commit step by step (sweepers, a batch of
	// projections) or whose post-commit re-promising only logs: a failure may
	// be swallowed or leave the earlier steps committed.
	bestEffort bool
	prepare    func(e *env) func(s *usecases.Service) error
}

func (e *env) offerID(tier shared.Tier) string {
	return offerOf(e.t, e.quote(tier), string(tier)).OfferID
}

func scenarios() []scenario {
	ctxm := func(e *env, typ string) usecases.Meta { return e.meta(typ) }
	return []scenario{
		{name: "quote", prepare: func(e *env) func(*usecases.Service) error {
			return func(s *usecases.Service) error {
				_, err := s.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{DestinationPostalPrefix: "787", Lines: lines()})
				return err
			}
		}},
		{name: "make promise", prepare: func(e *env) func(*usecases.Service) error {
			id := e.offerID(shared.TierNextDay)
			return func(s *usecases.Service) error {
				_, err := s.MakePromise(ctx, usecases.MakePromiseCommand{OfferID: id, Lines: lines()})
				return err
			}
		}},
		{name: "make promise replay", prepare: func(e *env) func(*usecases.Service) error {
			id := e.offerID(shared.TierNextDay)
			e.make(id)
			return func(s *usecases.Service) error {
				_, err := s.MakePromise(ctx, usecases.MakePromiseCommand{OfferID: id, Lines: lines()})
				return err
			}
		}},
		{name: "confirm", prepare: func(e *env) func(*usecases.Service) error {
			p := e.make(e.offerID(shared.TierNextDay)).Promise
			return func(s *usecases.Service) error { _, err := s.ConfirmPromise(ctx, p.PromiseID, "ord-1", 1); return err }
		}},
		{name: "get promise", prepare: func(e *env) func(*usecases.Service) error {
			p := e.make(e.offerID(shared.TierNextDay)).Promise
			return func(s *usecases.Service) error { _, err := s.GetPromise(ctx, p.PromiseID); return err }
		}},
		{name: "get offer", prepare: func(e *env) func(*usecases.Service) error {
			id := e.offerID(shared.TierNextDay)
			return func(s *usecases.Service) error { _, err := s.GetOffer(ctx, id); return err }
		}},
		{name: "list by order", prepare: func(e *env) func(*usecases.Service) error {
			e.confirmedNextDay("ord-1", 1)
			return func(s *usecases.Service) error { _, err := s.ListPromisesByOrder(ctx, "ord-1"); return err }
		}},
		{name: "list books", prepare: func(e *env) func(*usecases.Service) error {
			e.make(e.offerID(shared.TierNextDay))
			return func(s *usecases.Service) error { _, err := s.ListPromiseBooks(ctx, "N-AUS3", "2026-10-22"); return err }
		}},
		{name: "current policy", prepare: func(e *env) func(*usecases.Service) error {
			return func(s *usecases.Service) error { _, err := s.CurrentPolicy(ctx); return err }
		}},
		{name: "publish policy", prepare: func(e *env) func(*usecases.Service) error {
			return func(s *usecases.Service) error {
				_, err := s.PublishPromisePolicy(ctx, policy.Input{OfferTTLMinutes: 5, HoldTTLMinutes: 5, PackagesPerOrder: 1, SharePercent: 100})
				return err
			}
		}},
		{name: "release holds", prepare: func(e *env) func(*usecases.Service) error {
			e.make(e.offerID(shared.TierNextDay))
			e.clock.Advance(20 * time.Minute)
			return func(s *usecases.Service) error { _, err := s.ReleaseExpiredHolds(ctx, 10); return err }
		}},
		{name: "expire offers", bestEffort: true, prepare: func(e *env) func(*usecases.Service) error {
			e.quote()
			e.clock.Advance(20 * time.Minute)
			return func(s *usecases.Service) error { _, err := s.ExpireOffers(ctx, 10); return err }
		}},
		{name: "close books", prepare: func(e *env) func(*usecases.Service) error {
			e.make(e.offerID(shared.TierNextDay))
			e.clock.Set(at(22, 18, 0))
			return func(s *usecases.Service) error { _, err := s.CloseBooks(ctx, 10); return err }
		}},
		{name: "cancel", prepare: func(e *env) func(*usecases.Service) error {
			e.confirmedNextDay("ord-1", 1)
			return func(s *usecases.Service) error { return s.CancelPromises(ctx, "ord-1", nil, "CUSTOMER_REQUEST") }
		}},
		{name: "order placed", prepare: func(e *env) func(*usecases.Service) error {
			p := e.make(e.offerID(shared.TierNextDay)).Promise
			m := e.meta("CustomerOrderPlaced")
			return func(s *usecases.Service) error {
				return s.OnCustomerOrderPlaced(ctx, m, usecases.CustomerOrderPlaced{OrderID: "ord-1", Groups: []usecases.OrderGroup{{GroupNo: 1, PromiseID: p.PromiseID}}})
			}
		}},
		{name: "order cancelled", prepare: func(e *env) func(*usecases.Service) error {
			e.confirmedNextDay("ord-1", 1)
			m := e.meta("CustomerOrderCancelled")
			return func(s *usecases.Service) error {
				return s.OnCustomerOrderCancelled(ctx, m, usecases.CustomerOrderCancelled{OrderID: "ord-1", GroupNos: []int{1}})
			}
		}},
		{name: "departure cancelled with an alternative", bestEffort: true, prepare: func(e *env) func(*usecases.Service) error {
			e.confirmedNextDay("ord-1", 1)
			e.departure("dep-AUS3-1730", "lane-1", "PICKUP", "N-AUS3", "N-SAU1", at(22, 17, 30), at(22, 18, 45), 40)
			m := e.meta("DepartureCancelled")
			return func(s *usecases.Service) error {
				return s.OnDepartureCancelled(ctx, m, usecases.DepartureCancelled{DepartureID: "dep-AUS3-1800"})
			}
		}},
		{name: "departure cancelled without one", bestEffort: true, prepare: func(e *env) func(*usecases.Service) error {
			e.confirmedNextDay("ord-1", 1)
			must(e.t, e.svc.OnNodeStateChanged(ctx, e.meta("NodeSuspended"), usecases.NodeStateChanged{NodeID: "N-AUS3"}, readmodel.NodeSuspended))
			m := e.meta("DepartureCancelled")
			return func(s *usecases.Service) error {
				return s.OnDepartureCancelled(ctx, m, usecases.DepartureCancelled{DepartureID: "dep-AUS3-1800"})
			}
		}},
		{name: "wave cancelled", bestEffort: true, prepare: func(e *env) func(*usecases.Service) error {
			e.confirmedNextDay("ord-1", 1)
			m := e.meta("DeliveryWaveCancelled")
			return func(s *usecases.Service) error {
				return s.OnWaveCancelled(ctx, m, usecases.WaveCancelled{WaveID: "W-DAU1-1023-A"})
			}
		}},
		{name: "revise at risk", bestEffort: true, prepare: func(e *env) func(*usecases.Service) error {
			id := e.confirmedNextDay("ord-1", 1)
			must(e.t, e.svc.OnNodeStateChanged(ctx, e.meta("NodeSuspended"), usecases.NodeStateChanged{NodeID: "N-AUS3"}, readmodel.NodeSuspended))
			must(e.t, e.svc.OnDepartureCancelled(ctx, e.meta("DepartureCancelled"), usecases.DepartureCancelled{DepartureID: "dep-AUS3-1800"}))
			must(e.t, e.svc.OnNodeStateChanged(ctx, e.meta("NodeActivated"), usecases.NodeStateChanged{NodeID: "N-AUS3"}, readmodel.NodeActive))
			e.departure("dep-AUS3-1730", "lane-1", "PICKUP", "N-AUS3", "N-SAU1", at(22, 17, 30), at(22, 18, 45), 40)
			return func(s *usecases.Service) error { _, err := s.RepriceAtRisk(ctx, 10); _ = id; return err }
		}},
		{name: "booking failed", bestEffort: true, prepare: func(e *env) func(*usecases.Service) error {
			id := e.confirmedNextDay("ord-1", 1)
			must(e.t, e.svc.OnFulfillmentLocked(ctx, e.meta("FulfillmentLocked"), usecases.FulfillmentLocked{AssignmentID: "asg-1", OrderID: "ord-1", GroupNo: 1, PromiseID: id, NodeID: "N-AUS3"}))
			m := e.meta("BookingFailed")
			return func(s *usecases.Service) error {
				return s.OnBookingFailed(ctx, m, usecases.BookingFailed{AssignmentID: "asg-1", DepartureID: "dep-AUS3-1800"})
			}
		}},
		{name: "unsourceable", bestEffort: true, prepare: func(e *env) func(*usecases.Service) error {
			id := e.confirmedNextDay("ord-1", 1)
			m := e.meta("FulfillmentUnsourceable")
			return func(s *usecases.Service) error {
				return s.OnFulfillmentUnsourceable(ctx, m, usecases.FulfillmentUnsourceable{AssignmentID: "asg-1", OrderID: "ord-1", GroupNo: 1, PromiseID: id, ExcludedNodes: []string{"N-AUS3"}})
			}
		}},
		{name: "judge on delivery", prepare: func(e *env) func(*usecases.Service) error {
			id := e.confirmedNextDay("ord-1", 1)
			lockAndShip(e.t, e, id, "TRK-1")
			m := e.meta("PackageDelivered")
			return func(s *usecases.Service) error {
				return s.OnPackageDelivered(ctx, m, usecases.PackageDelivered{TrackingNumber: "TRK-1", DeliveredAt: at(23, 12, 0)})
			}
		}},
		{name: "judge on lock", prepare: func(e *env) func(*usecases.Service) error {
			id := e.confirmedNextDay("ord-1", 1)
			must(e.t, e.svc.OnFulfillmentShipped(ctx, e.meta("FulfillmentShipped"), usecases.FulfillmentShipped{AssignmentID: "asg-1", TrackingNumbers: []string{"TRK-1"}}))
			must(e.t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-1", AssignmentID: "asg-1", DeliveredAt: at(23, 12, 0)}))
			m := e.meta("FulfillmentLocked")
			return func(s *usecases.Service) error {
				return s.OnFulfillmentLocked(ctx, m, usecases.FulfillmentLocked{AssignmentID: "asg-1", OrderID: "ord-1", GroupNo: 1, PromiseID: id, NodeID: "N-AUS3"})
			}
		}},
		{name: "package lost", prepare: func(e *env) func(*usecases.Service) error {
			id := e.confirmedNextDay("ord-1", 1)
			lockAndShip(e.t, e, id, "TRK-1")
			m := e.meta("PackageLost")
			return func(s *usecases.Service) error {
				return s.OnPackageLost(ctx, m, usecases.PackageLost{TrackingNumber: "TRK-1", DeclaredAt: at(23, 10, 0)})
			}
		}},
		{name: "projections", bestEffort: true, prepare: func(e *env) func(*usecases.Service) error {
			m := func(t string) usecases.Meta { return ctxm(e, t) }
			return func(s *usecases.Service) error {
				calls := []func() error{
					func() error {
						return s.OnNodeRegistered(ctx, m("a"), usecases.NodeRegistered{NodeID: "N-NEW", NodeType: "FULFILLMENT_CENTER", Timezone: "America/Chicago"})
					},
					func() error {
						p := fcProfile(60, 10)
						p.NodeID, p.Timezone, p.ProfileRevision = "N-NEW", "America/Chicago", 1
						return s.OnNodeProfileDeclared(ctx, m("b"), *p)
					},
					func() error {
						return s.OnNodeStateChanged(ctx, m("c"), usecases.NodeStateChanged{NodeID: "N-NEW"}, readmodel.NodeActive)
					},
					func() error {
						return s.OnLaneOpened(ctx, m("d"), usecases.LaneOpened{LaneID: "lane-9", OriginNodeID: "a", DestinationNodeID: "b"})
					},
					func() error { return s.OnLaneClosed(ctx, m("e"), usecases.LaneClosed{LaneID: "lane-9"}) },
					func() error {
						return s.OnServiceAreaAssigned(ctx, m("f"), usecases.ServiceAreaAssigned{PostalPrefix: "786", DeliveryStationNodeID: "N-DAU1", AssignmentVersion: 1})
					},
					func() error {
						return s.OnRegionDefined(ctx, m("g"), usecases.RegionDefined{RegionID: "R-X", Code: "X", Name: "X"})
					},
					func() error {
						return s.OnCatalogItemChanged(ctx, m("h"), usecases.CatalogItemChanged{CatalogItemID: "ci-2"}, false)
					},
					func() error {
						return s.OnListingCreated(ctx, m("i"), usecases.ListingCreated{ListingID: "lst-9", CatalogItemID: "ci-2", NodeID: "N-AUS3"})
					},
					func() error {
						return s.OnListingAvailabilityChanged(ctx, m("j"), usecases.ListingAvailabilityChanged{ListingID: "lst-9", CatalogItemID: "ci-2", NodeID: "N-AUS3", AvailableQty: 3, AsOf: at(22, 14, 1)})
					},
					func() error {
						return s.OnListingToggled(ctx, m("k"), usecases.ListingToggled{ListingID: "lst-9", NodeID: "N-AUS3"}, true)
					},
					func() error {
						return s.OnScorecardUpdated(ctx, m("l"), usecases.ScorecardUpdated{NodeID: "N-AUS3", Window: "30d", Grade: "A", Observations: 9, ComputedAt: at(22, 14, 0)})
					},
					func() error {
						return s.OnDepartureScheduled(ctx, m("m"), usecases.DepartureScheduled{DepartureID: "dep-9", LaneID: "lane-1", Kind: "PICKUP", OriginNodeID: "N-AUS3", DestinationNodeID: "N-SAU1", CutoffAt: at(22, 20, 0), ArrivesAt: at(22, 21, 0), CapacityPackages: 5})
					},
					func() error {
						return s.OnDepartureCapacityChanged(ctx, m("n"), usecases.DepartureCapacityChanged{DepartureID: "dep-9", RemainingPackages: 1})
					},
					func() error {
						return s.OnWaveScheduled(ctx, m("o"), usecases.WaveScheduled{WaveID: "W-9", StationNodeID: "N-DAU1", WindowStart: at(24, 9, 0), WindowEnd: at(24, 12, 0), StopCapacity: 10})
					},
					func() error {
						return s.OnWaveCapacityChanged(ctx, m("p"), usecases.WaveCapacityChanged{WaveID: "W-9", RemainingStops: 1})
					},
					func() error {
						return s.OnFulfillmentShipped(ctx, m("q"), usecases.FulfillmentShipped{AssignmentID: "asg-9", TrackingNumbers: []string{"T9"}})
					},
					func() error {
						return s.OnPackageReturned(ctx, m("r"), usecases.PackageReturned{TrackingNumber: "T9", ReturnedAt: at(24, 9, 0)})
					},
					func() error {
						return s.OnPackageException(ctx, m("s"), usecases.PackageException{AssignmentID: "asg-9", TrackingNumber: "T9"})
					},
					func() error {
						return s.OnInductionDeferred(ctx, m("t"), usecases.InductionDeferred{AssignmentID: "asg-9", TrackingNumber: "T9"})
					},
					func() error {
						return s.OnBookingMoved(ctx, m("u"), usecases.BookingMoved{AssignmentID: "asg-9", FromDepartureID: "d"})
					},
				}
				for _, call := range calls {
					if err := call(); err != nil {
						return err
					}
				}
				return nil
			}
		}},
	}
}

// sweep fails the n-th port call of the scenario for every n it makes and
// checks the outcome of each; it returns a description of the first violation.
func sweep(t *testing.T, sc scenario) string {
	t.Helper()
	for n := 1; n < 400; n++ {
		e := newEnv(t)
		call := sc.prepare(e)
		f := &faults{failAt: n}
		before := e.store.Counts()
		err := call(e.faulty(f))
		if f.calls < n {
			return reachedEnd(n, err)
		}
		if msg := judgeFailure(sc, n, err, before, e.store.Counts()); msg != "" {
			return msg
		}
	}
	return "more than 400 port calls"
}

func reachedEnd(n int, err error) string {
	if err != nil {
		return fmt.Sprintf("call %d never reached yet failed: %v", n, err)
	}
	if n == 1 {
		return "the scenario makes no port call"
	}
	return ""
}

func judgeFailure(sc scenario, n int, err error, before, after memory.Counts) string {
	if err == nil {
		if sc.bestEffort {
			return ""
		}
		return fmt.Sprintf("failing port call %d was swallowed", n)
	}
	if !errors.Is(err, errInjected) {
		return fmt.Sprintf("failing port call %d returned %v", n, err)
	}
	if after != before && !sc.bestEffort {
		return fmt.Sprintf("failing port call %d left a trace: %+v -> %+v", n, before, after)
	}
	return ""
}

// TestEveryPortFailureSurfacesAndLeavesNoTrace fails the n-th port call of each
// use case in turn, for every n the use case makes, and requires the failure to
// be returned (or only logged for the best-effort re-promise after commit) and,
// for a returned failure, the stored state to be exactly what it was.
func TestEveryPortFailureSurfacesAndLeavesNoTrace(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			if msg := sweep(t, sc); msg != "" {
				t.Fatal(msg)
			}
		})
	}
}
