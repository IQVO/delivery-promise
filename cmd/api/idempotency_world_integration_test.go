//go:build integration

package main

// The world of the idempotency replay-class tests: the real service wiring of
// buildService (Postgres repositories, outbox, unit of work) behind the real
// router and the real Idempotency-Key middleware, on a testcontainers Postgres.
// No Kafka: the read models are projected through the consumer use cases, as
// the usecases tests do, and the clock is fixed so an offer never expires
// under the test.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/delivery-promise/internal/adapters/inbound/http"
	outboundkafka "github.com/claudioed/delivery-promise/internal/adapters/outbound/kafka"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

var (
	cdt = time.FixedZone("CDT", -5*3600)
	bg  = context.Background()
)

// at is a day of the worked example (October 2026) in Austin (CDT, UTC-5).
func at(day, h, m int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, cdt).UTC() }

// fixedClock is the service's "now": Thursday 2026-10-22 14:05 Austin.
type fixedClock struct{}

func (fixedClock) Now() time.Time { return at(22, 14, 5) }

// contendedPolicies stands for a policy publisher that keeps losing the
// optimistic-lock race: while contended is set every Save answers
// ports.ErrConcurrentUpdate without writing, as a guarded UPDATE that matches
// no row does. The use case retries ports.MaxAttempts times and gives up.
type contendedPolicies struct {
	ports.PolicyRepository
	contended atomic.Bool
	saves     atomic.Int32
}

func (c *contendedPolicies) Save(ctx context.Context, p *policy.Policy) error {
	c.saves.Add(1)
	if c.contended.Load() {
		return ports.ErrConcurrentUpdate
	}
	return c.PolicyRepository.Save(ctx, p)
}

// world is the service under test: the router and what a test inspects.
type world struct {
	t        *testing.T
	pool     *pgtest.Pool
	svc      *usecases.Service
	router   http.Handler
	policies *contendedPolicies
	events   int
}

type answer struct {
	code   int
	header http.Header
	body   string
}

func (a answer) replayed() bool { return a.header.Get(inboundhttp.IdempotencyReplayedHeader) == "true" }

func (a answer) slug() string {
	var p struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal([]byte(a.body), &p)
	return p.Type[strings.LastIndex(p.Type, "/")+1:]
}

func (a answer) field(name string) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(a.body), &m)
	v, _ := m[name].(string)
	return v
}

func newWorld(t *testing.T) *world {
	t.Helper()
	pool := pgtest.NewPool(t)
	outbox := postgres.NewOutbox(pool, outboundkafka.NewEncoder())
	svc := buildService(pool, outbox, slog.New(slog.DiscardHandler), nil)
	svc.Clock = fixedClock{}
	w := &world{t: t, pool: pool, svc: svc, policies: &contendedPolicies{PolicyRepository: svc.Policies}}
	svc.Policies = w.policies
	w.router = inboundhttp.NewRouter(&inboundhttp.Server{
		API:         svc,
		Idempotency: inboundhttp.RequireIdempotencyKey(pool, postgres.BindTx),
	}, slog.New(slog.DiscardHandler), "")
	return w
}

func (w *world) post(path, key, body string) answer {
	w.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(inboundhttp.IdempotencyKeyHeader, key)
	rec := httptest.NewRecorder()
	w.router.ServeHTTP(rec, req)
	out, _ := io.ReadAll(rec.Result().Body)
	return answer{code: rec.Code, header: rec.Result().Header, body: string(out)}
}

func (w *world) get(path string) answer {
	w.t.Helper()
	rec := httptest.NewRecorder()
	w.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	out, _ := io.ReadAll(rec.Result().Body)
	return answer{code: rec.Code, header: rec.Result().Header, body: string(out)}
}

func (w *world) keyRows(key string) int {
	w.t.Helper()
	var n int
	if err := w.pool.QueryRow(bg, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

func (w *world) meta(typ string) usecases.Meta {
	w.events++
	return usecases.Meta{ID: fmt.Sprintf("evt-%04d", w.events), Type: typ, Time: fixedClock{}.Now()}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func hoursDTO(start, end string) usecases.HoursDTO { return usecases.HoursDTO{Start: start, End: end} }

func allWeek(start, end string) map[string]usecases.HoursDTO {
	m := map[string]usecases.HoursDTO{}
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
		m[d] = hoursDTO(start, end)
	}
	return m
}

func (w *world) node(id, typ, subtype string, profile *usecases.NodeProfileDeclared) {
	w.t.Helper()
	must(w.t, w.svc.OnNodeRegistered(bg, w.meta("NodeRegistered"), usecases.NodeRegistered{
		NodeID: id, NodeType: typ, Subtype: subtype, Operator: "VENDOR", RegionID: "R-SC", Timezone: "America/Chicago",
	}))
	if profile != nil {
		profile.NodeID, profile.Timezone = id, "America/Chicago"
		must(w.t, w.svc.OnNodeProfileDeclared(bg, w.meta("NodeOperationalProfileDeclared"), *profile))
	}
	must(w.t, w.svc.OnNodeStateChanged(bg, w.meta("NodeActivated"), usecases.NodeStateChanged{NodeID: id}, "ACTIVE"))
}

// seedNetwork projects the worked example of the promise model: one
// fulfilment centre whose 18:00 pickup book holds ONE order (share 1), a sort
// centre, a delivery station, two lanes, a pickup, a linehaul and a wave, and
// 37 units of ci-1001 at the fulfilment centre.
func (w *world) seedNetwork() {
	w.t.Helper()
	w.node("N-DAU1", "DELIVERY_STATION", "", nil)
	w.node("N-SAU1", "SORTATION_CENTER", "", &usecases.NodeProfileDeclared{
		ProfileRevision: 1, OperatingHoursByDay: allWeek("00:00", "23:59"), SortDwellMinutes: 120,
		ThroughputCap: usecases.ThroughputCapDTO{Value: 9999, TimeUnit: "HOUR"},
	})
	w.node("N-AUS3", "FULFILLMENT_CENTER", "SORTABLE", &usecases.NodeProfileDeclared{
		ProfileRevision: 1, OperatingHoursByDay: allWeek("06:00", "22:00"), HandlingTimeMinutes: 95,
		Cutoffs:       []usecases.CutoffDTO{{LocalTime: "11:00"}, {LocalTime: "18:00", MaxOrders: 1}},
		ThroughputCap: usecases.ThroughputCapDTO{Value: 400, TimeUnit: "HOUR"},
	})
	must(w.t, w.svc.OnRegionDefined(bg, w.meta("RegionDefined"), usecases.RegionDefined{RegionID: "R-SC", Code: "SC", Name: "South Central"}))
	must(w.t, w.svc.OnServiceAreaAssigned(bg, w.meta("ServiceAreaAssigned"), usecases.ServiceAreaAssigned{PostalPrefix: "787", DeliveryStationNodeID: "N-DAU1", RegionID: "R-SC", AssignmentVersion: 1}))
	must(w.t, w.svc.OnLaneOpened(bg, w.meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-1", OriginNodeID: "N-AUS3", DestinationNodeID: "N-SAU1", NominalTransitMinutes: 75}))
	must(w.t, w.svc.OnLaneOpened(bg, w.meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-2", OriginNodeID: "N-SAU1", DestinationNodeID: "N-DAU1", NominalTransitMinutes: 90}))
	must(w.t, w.svc.OnCatalogItemChanged(bg, w.meta("CatalogItemCreated"), usecases.CatalogItemChanged{CatalogItemID: "ci-1001"}, false))
	must(w.t, w.svc.OnListingCreated(bg, w.meta("ListingCreated"), usecases.ListingCreated{ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-AUS3"}))
	must(w.t, w.svc.OnListingAvailabilityChanged(bg, w.meta("ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-AUS3", AvailableQty: 37, AsOf: at(22, 14, 0),
	}))
	dep := func(id, lane, kind, from, to string, cutoff, arrives time.Time, capacity int) {
		must(w.t, w.svc.OnDepartureScheduled(bg, w.meta("DepartureScheduled"), usecases.DepartureScheduled{
			DepartureID: id, LaneID: lane, Kind: kind, OriginNodeID: from, DestinationNodeID: to, CutoffAt: cutoff,
			DepartsAt: cutoff.Add(15 * time.Minute), ArrivesAt: arrives, CapacityPackages: capacity,
		}))
	}
	dep("dep-AUS3-1800", "lane-1", "PICKUP", "N-AUS3", "N-SAU1", at(22, 18, 0), at(22, 19, 15), 40)
	dep("dep-SAU1-DAU1-2200", "lane-2", "LINEHAUL", "N-SAU1", "N-DAU1", at(22, 22, 0), at(22, 23, 30), 140)
	must(w.t, w.svc.OnWaveScheduled(bg, w.meta("DeliveryWaveScheduled"), usecases.WaveScheduled{
		WaveID: "W-DAU1-1023-A", StationNodeID: "N-DAU1", InductCutoffAt: at(23, 6, 0), WindowStart: at(23, 9, 0), WindowEnd: at(23, 21, 0), StopCapacity: 320,
	}))
}

// nextDayOffer quotes ci-1001 x2 for prefix 787 and returns the NEXT_DAY offer id.
func (w *world) nextDayOffer() string {
	w.t.Helper()
	res, err := w.svc.QuoteDeliveryOffers(bg, usecases.QuoteCommand{
		DestinationPostalPrefix: "787", Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}}, Tiers: []shared.Tier{shared.TierNextDay},
	})
	must(w.t, err)
	for _, o := range res.Offers {
		if o.Tier == string(shared.TierNextDay) {
			return o.OfferID
		}
	}
	w.t.Fatalf("no NEXT_DAY offer: offers %+v unavailable %+v", res.Offers, res.Unavailable)
	return ""
}

func makePromiseBody(offerID string) string {
	return fmt.Sprintf(`{"offerId":%q,"lines":[{"catalogItemId":"ci-1001","quantity":2}]}`, offerID)
}
