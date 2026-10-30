package a2a_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	inbounda2a "github.com/claudioed/delivery-promise/internal/adapters/inbound/a2a"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/memory"
	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// now is the fixture's fixed "now": Thursday 2026-10-22 14:05 Austin time, the
// worked example of the promise model.
var now = time.Date(2026, 10, 22, 14, 5, 0, 0, time.FixedZone("CDT", -5*3600)).UTC()

const problemBase = "https://retailnetwork.example/problems/"

// harness serves the real A2A handler over httptest, wired to a real
// usecases.Service on the in-memory doubles exactly as cmd/a2a wires it (minus
// Postgres). The write use cases (consumer projections, quote, make, confirm,
// publish) exist ONLY here, to seed data the way cmd/api and checkout would:
// the A2A surface itself has none.
type harness struct {
	store     *memory.Store
	promiseID string
	wire      *wire
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := memory.NewStore()
	clock := memory.NewFakeClock(now)
	svc := usecases.New(usecases.Deps{
		UoW: store.UnitOfWork(), Offers: store.Offers(), Promises: store.Promises(), Books: store.Books(), Policies: store.Policies(),
		Network: store.Network(), Shipments: store.Shipments(), Outbox: store.Outbox(), Inbox: store.Inbox(),
		Clock: clock, IDs: &memory.SeqIDs{},
	})
	h := &harness{store: store}
	h.promiseID = h.projectWorkedExample(t, svc, clock)
	h.wire = newWire(t, svc)
	return h
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func at(day, h, m int) time.Time {
	return time.Date(2026, 10, day, h, m, 0, 0, time.FixedZone("CDT", -5*3600)).UTC()
}

func hours(start, end string) usecases.HoursDTO { return usecases.HoursDTO{Start: start, End: end} }

func allWeek(start, end string) map[string]usecases.HoursDTO {
	m := map[string]usecases.HoursDTO{}
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
		m[d] = hours(start, end)
	}
	return m
}

// projectWorkedExample feeds the local read models through the REAL consumer
// use cases (the way cmd/api's Kafka consumer does): the nodes, region,
// service area, lanes, stock, the pickup departure and the delivery wave of
// the worked example, then quotes and confirms one NEXT_DAY promise for
// order ord-1001 and publishes policy v1.
func (h *harness) projectWorkedExample(t *testing.T, svc *usecases.Service, clock *memory.FakeClock) string {
	t.Helper()
	ctx := context.Background()
	n := 0
	meta := func(typ string) usecases.Meta {
		n++
		return usecases.Meta{ID: fmt.Sprintf("evt-%04d", n), Type: typ, Time: clock.Now()}
	}

	must(t, svc.OnNodeRegistered(ctx, meta("NodeRegistered"), usecases.NodeRegistered{
		NodeID: "N-DAU1", NodeType: "DELIVERY_STATION", Operator: "VENDOR", RegionID: "R-SC", Timezone: "America/Chicago",
	}))
	must(t, svc.OnNodeRegistered(ctx, meta("NodeRegistered"), usecases.NodeRegistered{
		NodeID: "N-SAU1", NodeType: "SORTATION_CENTER", Operator: "VENDOR", RegionID: "R-SC", Timezone: "America/Chicago",
	}))
	must(t, svc.OnNodeRegistered(ctx, meta("NodeRegistered"), usecases.NodeRegistered{
		NodeID: "N-AUS3", NodeType: "FULFILLMENT_CENTER", Subtype: "SORTABLE", Operator: "VENDOR", RegionID: "R-SC", Timezone: "America/Chicago",
	}))
	must(t, svc.OnNodeProfileDeclared(ctx, meta("NodeOperationalProfileDeclared"), usecases.NodeProfileDeclared{
		NodeID: "N-AUS3", ProfileRevision: 1, Timezone: "America/Chicago", OperatingHoursByDay: allWeek("06:00", "22:00"),
		HandlingTimeMinutes: 95, Cutoffs: []usecases.CutoffDTO{{LocalTime: "11:00"}, {LocalTime: "18:00", MaxOrders: 2500}},
		ThroughputCap: usecases.ThroughputCapDTO{Value: 400, TimeUnit: "HOUR"},
	}))
	must(t, svc.OnNodeStateChanged(ctx, meta("NodeActivated"), usecases.NodeStateChanged{NodeID: "N-AUS3"}, "ACTIVE"))
	must(t, svc.OnNodeStateChanged(ctx, meta("NodeActivated"), usecases.NodeStateChanged{NodeID: "N-SAU1"}, "ACTIVE"))
	must(t, svc.OnNodeStateChanged(ctx, meta("NodeActivated"), usecases.NodeStateChanged{NodeID: "N-DAU1"}, "ACTIVE"))
	must(t, svc.OnRegionDefined(ctx, meta("RegionDefined"), usecases.RegionDefined{RegionID: "R-SC", Code: "SC", Name: "South Central"}))
	must(t, svc.OnServiceAreaAssigned(ctx, meta("ServiceAreaAssigned"), usecases.ServiceAreaAssigned{
		PostalPrefix: "787", DeliveryStationNodeID: "N-DAU1", RegionID: "R-SC", AssignmentVersion: 1,
	}))
	must(t, svc.OnLaneOpened(ctx, meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-1", OriginNodeID: "N-AUS3", DestinationNodeID: "N-SAU1", NominalTransitMinutes: 75}))
	must(t, svc.OnLaneOpened(ctx, meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-2", OriginNodeID: "N-SAU1", DestinationNodeID: "N-DAU1", NominalTransitMinutes: 90}))
	must(t, svc.OnCatalogItemChanged(ctx, meta("CatalogItemCreated"), usecases.CatalogItemChanged{CatalogItemID: "ci-1001"}, false))
	must(t, svc.OnListingCreated(ctx, meta("ListingCreated"), usecases.ListingCreated{ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-AUS3"}))
	must(t, svc.OnListingAvailabilityChanged(ctx, meta("ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-AUS3", AvailableQty: 37, AsOf: at(22, 14, 0),
	}))
	must(t, svc.OnDepartureScheduled(ctx, meta("DepartureScheduled"), usecases.DepartureScheduled{
		DepartureID: "dep-AUS3-1800", LaneID: "lane-1", Kind: "PICKUP", OriginNodeID: "N-AUS3", DestinationNodeID: "N-SAU1",
		CutoffAt: at(22, 18, 0), DepartsAt: at(22, 18, 15), ArrivesAt: at(22, 19, 15), CapacityPackages: 40,
	}))
	must(t, svc.OnDepartureScheduled(ctx, meta("DepartureScheduled"), usecases.DepartureScheduled{
		DepartureID: "dep-SAU1-DAU1-2200", LaneID: "lane-2", Kind: "LINEHAUL", OriginNodeID: "N-SAU1", DestinationNodeID: "N-DAU1",
		CutoffAt: at(22, 22, 0), DepartsAt: at(22, 22, 15), ArrivesAt: at(22, 23, 30), CapacityPackages: 140,
	}))
	must(t, svc.OnWaveScheduled(ctx, meta("DeliveryWaveScheduled"), usecases.WaveScheduled{
		WaveID: "W-DAU1-1023-A", StationNodeID: "N-DAU1", InductCutoffAt: at(23, 6, 0), WindowStart: at(23, 9, 0), WindowEnd: at(23, 21, 0), StopCapacity: 320,
	}))

	// checkout: quote, make and confirm the worked-example NEXT_DAY promise.
	quote, err := svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{
		DestinationPostalPrefix: "787",
		Lines:                   []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
		Tiers:                   []shared.Tier{shared.TierNextDay},
	})
	must(t, err)
	if len(quote.Offers) != 1 {
		t.Fatalf("worked example quotes one NEXT_DAY offer, got %+v (unavailable %+v)", quote.Offers, quote.Unavailable)
	}
	made, err := svc.MakePromise(ctx, usecases.MakePromiseCommand{
		OfferID: quote.Offers[0].OfferID, Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
	})
	must(t, err)
	_, err = svc.ConfirmPromise(ctx, made.Promise.PromiseID, "ord-1001", 1)
	must(t, err)

	// the operator publishes policy v1.
	_, err = svc.PublishPromisePolicy(ctx, policy.Input{
		OfferTTLMinutes: 5, HoldTTLMinutes: 20, PackagesPerOrder: 2, SharePercent: 50,
		Deadlines: []policy.Deadline{{Tier: shared.TierNextDay, RegionID: "R-SC", Minute: 23*60 + 59}},
	})
	must(t, err)
	return made.Promise.PromiseID
}

// ---- the wire: JSON-RPC over HTTP, driven by the SDK's own client ----------

// wireEnvelope is a TEST-LOCAL copy of the answer contract. If the adapter's
// envelope drifts, this suite fails instead of silently following it.
type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  *struct {
		PromiseStatus *struct {
			OrderID  string `json:"orderId"`
			Promises []struct {
				PromiseID string `json:"promiseId"`
				OrderID   string `json:"orderId"`
				GroupNo   int    `json:"groupNo"`
				State     string `json:"state"`
				Terms     struct {
					Tier string `json:"tier"`
				} `json:"terms"`
				WitnessPath struct {
					NodeID string `json:"nodeId"`
				} `json:"witnessPath"`
			} `json:"promises"`
		} `json:"promiseStatus"`
		BookCapacity *struct {
			NodeID string `json:"nodeId"`
			Date   string `json:"date"`
			Books  []struct {
				BookID   string `json:"bookId"`
				NodeID   string `json:"nodeId"`
				State    string `json:"state"`
				Capacity struct {
					Share       int `json:"share"`
					Provisional int `json:"provisional"`
					Confirmed   int `json:"confirmed"`
				} `json:"capacity"`
			} `json:"books"`
		} `json:"bookCapacity"`
	} `json:"data"`
	Problem *struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Status int    `json:"status"`
	} `json:"problem"`
}

type wire struct {
	client *a2aclient.Client
}

func newWire(t *testing.T, reads inbounda2a.Reads) *wire {
	t.Helper()
	srv := httptest.NewServer(inbounda2a.Handler(inbounda2a.NewExecutor(inbounda2a.Deps{Reads: reads})))
	t.Cleanup(srv.Close)
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return &wire{client: client}
}

// answer is everything one reply carried: the decoded envelope, the raw
// envelope JSON and the one-line text summary.
type answer struct {
	env  wireEnvelope
	raw  string
	text string
}

// all returns every string the reply exposes to the caller.
func (a answer) all() string { return a.raw + " " + a.text }

// call sends one message part and returns the agent's reply. A JSON-RPC
// transport error fails the test: every rejection must be answered in-band.
func (w *wire) call(t *testing.T, part *a2a.Part) answer {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("SendMessage returned a transport error, want an in-band answer: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env wireEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, raw)
	}
	text, _ := msg.Parts[1].Content.(a2a.Text)
	return answer{env: env, raw: string(raw), text: string(text)}
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) answer {
	t.Helper()
	return w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
}

func wantProblem(t *testing.T, a answer, status int, slug string) {
	t.Helper()
	if a.env.Problem == nil {
		t.Fatalf("expected a %d %s problem, got data: %s", status, slug, a.raw)
	}
	if a.env.Problem.Status != status || a.env.Problem.Type != problemBase+slug {
		t.Fatalf("problem = %+v, want %d %s", a.env.Problem, status, slug)
	}
	if a.env.Data != nil {
		t.Fatalf("a problem answer must carry no data: %s", a.raw)
	}
	if !strings.Contains(a.text, fmt.Sprintf("(%d)", status)) {
		t.Fatalf("summary %q does not carry the status %d", a.text, status)
	}
}

func TestWirePromiseStatusByOrder(t *testing.T) {
	h := newHarness(t)
	a := h.wire.skill(t, "promise_status", map[string]any{"orderId": "ord-1001"})
	ps := a.env.Data.PromiseStatus
	if a.env.Problem != nil || a.env.Skill != "promise_status" || ps == nil || ps.OrderID != "ord-1001" {
		t.Fatalf("envelope = %s", a.raw)
	}
	if len(ps.Promises) != 1 {
		t.Fatalf("promises = %d, want the one confirmed promise of the worked example", len(ps.Promises))
	}
	p := ps.Promises[0]
	if p.PromiseID != h.promiseID || p.State != "CONFIRMED" || p.OrderID != "ord-1001" || p.GroupNo != 1 ||
		p.Terms.Tier != "NEXT_DAY" || p.WitnessPath.NodeID != "N-AUS3" {
		t.Fatalf("promise = %+v", p)
	}
	if a.text != "order ord-1001 has 1 promise(s): 1 CONFIRMED" {
		t.Fatalf("summary = %q", a.text)
	}
}

func TestWirePromiseStatusByPromiseID(t *testing.T) {
	h := newHarness(t)
	a := h.wire.skill(t, "promise_status", map[string]any{"promiseId": h.promiseID})
	ps := a.env.Data.PromiseStatus
	if a.env.Problem != nil || ps == nil || len(ps.Promises) != 1 || ps.OrderID != "" {
		t.Fatalf("envelope = %s", a.raw)
	}
	if ps.Promises[0].PromiseID != h.promiseID || ps.Promises[0].State != "CONFIRMED" {
		t.Fatalf("promise = %+v", ps.Promises[0])
	}
	if want := fmt.Sprintf("promise %s is CONFIRMED", h.promiseID); a.text != want {
		t.Fatalf("summary = %q, want %q", a.text, want)
	}
}

// An order without a promise is an empty list, exactly like REST, never a
// problem and never a null.
func TestWirePromiseStatusUnknownOrderIsAnEmptyList(t *testing.T) {
	a := newHarness(t).wire.skill(t, "promise_status", map[string]any{"orderId": "ord-9999"})
	if a.env.Problem != nil || a.env.Data.PromiseStatus == nil || len(a.env.Data.PromiseStatus.Promises) != 0 {
		t.Fatalf("envelope = %s", a.raw)
	}
	if !strings.Contains(a.raw, `"promises":[]`) {
		t.Fatalf("the empty list must serialise as [] not null: %s", a.raw)
	}
	if a.text != "order ord-9999 has no promise" {
		t.Fatalf("summary = %q", a.text)
	}
}

func TestWireBookCapacity(t *testing.T) {
	h := newHarness(t)
	a := h.wire.skill(t, "book_capacity", map[string]any{"nodeId": "N-AUS3", "date": "2026-10-22"})
	bc := a.env.Data.BookCapacity
	if a.env.Problem != nil || a.env.Skill != "book_capacity" || bc == nil || bc.NodeID != "N-AUS3" || bc.Date != "2026-10-22" {
		t.Fatalf("envelope = %s", a.raw)
	}
	if len(bc.Books) != 1 {
		t.Fatalf("books = %d, want the 18:00 pickup book", len(bc.Books))
	}
	b := bc.Books[0]
	if b.BookID != "N-AUS3:dep-AUS3-1800" || b.NodeID != "N-AUS3" || b.Capacity.Confirmed != 1 || b.Capacity.Provisional != 0 || b.Capacity.Share == 0 {
		t.Fatalf("book = %+v, want the confirmed hold of the worked example on a book with a share", b)
	}
	want := fmt.Sprintf("node N-AUS3 has 1 promise book(s) on 2026-10-22: N-AUS3:dep-AUS3-1800 %s share %d, provisional 0, confirmed 1", b.State, b.Capacity.Share)
	if a.text != want {
		t.Fatalf("summary = %q, want %q", a.text, want)
	}
}

func TestWireBookCapacityWithoutBooksIsAnEmptyList(t *testing.T) {
	h := newHarness(t)
	for _, args := range []map[string]any{
		{"nodeId": "N-AUS3", "date": "2026-10-23"}, // no cutoff that day
		{"nodeId": "N-NOPE", "date": "2026-10-22"}, // unknown node: same as REST, empty
	} {
		a := h.wire.skill(t, "book_capacity", args)
		if a.env.Problem != nil || a.env.Data.BookCapacity == nil || len(a.env.Data.BookCapacity.Books) != 0 || !strings.Contains(a.raw, `"books":[]`) {
			t.Fatalf("%v: envelope = %s", args, a.raw)
		}
		if !strings.Contains(a.text, "has no promise book on") {
			t.Fatalf("summary = %q", a.text)
		}
	}
}

// A Text part holding the same JSON is accepted, like a Data part.
func TestWireAcceptsATextPartHoldingTheSkillJSON(t *testing.T) {
	h := newHarness(t)
	a := h.wire.call(t, a2a.NewTextPart(`{"skill":"promise_status","arguments":{"orderId":"ord-1001"}}`))
	if a.env.Problem != nil || a.env.Data.PromiseStatus == nil || len(a.env.Data.PromiseStatus.Promises) != 1 {
		t.Fatalf("envelope = %s", a.raw)
	}
}

// The reply is bound to the caller's context.
func TestWireReplyKeepsTheCallersContextID(t *testing.T) {
	h := newHarness(t)
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": "promise_status", "arguments": map[string]any{"orderId": "ord-1001"}}))
	msg.ContextID = "ctx-saga-7"
	resp, err := h.wire.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatal(err)
	}
	if reply, ok := resp.(*a2a.Message); !ok || reply.ContextID != "ctx-saga-7" {
		t.Fatalf("reply = %+v, want contextId ctx-saga-7", resp)
	}
}

// ---- argument validation: every bad request is an in-band 400 -----------------

func TestWireArgumentValidation(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		name string
		part *a2a.Part
		slug string
	}{
		{"both orderId and promiseId", skillPart("promise_status", `{"orderId":"ord-1001","promiseId":"prm-1"}`), "invalid-argument"},
		{"neither orderId nor promiseId", skillPart("promise_status", `{}`), "invalid-argument"},
		{"blank orderId", skillPart("promise_status", `{"orderId":"   "}`), "invalid-argument"},
		{"no arguments at all", a2a.NewDataPart(map[string]any{"skill": "promise_status"}), "invalid-argument"},
		{"unknown argument (typo)", skillPart("promise_status", `{"ordrId":"ord-1001"}`), "invalid-argument"},
		{"unknown extra argument", skillPart("promise_status", `{"orderId":"ord-1001","limit":5}`), "invalid-argument"},
		{"wrong argument type", skillPart("promise_status", `{"orderId":1001}`), "invalid-argument"},
		{"arguments not an object", skillPart("promise_status", `["ord-1001"]`), "invalid-argument"},
		{"book_capacity without nodeId", skillPart("book_capacity", `{"date":"2026-10-22"}`), "invalid-argument"},
		{"book_capacity without date", skillPart("book_capacity", `{"nodeId":"N-AUS3"}`), "invalid-argument"},
		{"book_capacity unknown argument", skillPart("book_capacity", `{"nodeId":"N-AUS3","date":"2026-10-22","day":"x"}`), "invalid-argument"},
		{"unknown skill", skillPart("get_current_policy", `{}`), "unknown-skill"},
		{"a write-verb skill is not offered", skillPart("confirm_promise", `{"promiseId":"prm-1","orderId":"ord-1"}`), "unknown-skill"},
		{"no skill field", a2a.NewDataPart(map[string]any{"arguments": map[string]any{"orderId": "ord-1001"}}), "malformed-request"},
		{"unknown top-level field", a2a.NewDataPart(map[string]any{"skill": "promise_status", "arguments": map[string]any{"orderId": "o"}, "extra": 1}), "malformed-request"},
		{"text part is not JSON", a2a.NewTextPart("how is my order doing?"), "malformed-request"},
		{"trailing data after the JSON document", a2a.NewTextPart(`{"skill":"promise_status","arguments":{"orderId":"ord-1001"}} {"skill":"x"}`), "malformed-request"},
		{"a text part with a JSON array", a2a.NewTextPart(`[]`), "malformed-request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantProblem(t, h.wire.call(t, c.part), 400, c.slug)
		})
	}
}

// Arguments of a text part are strict too: trailing data inside the arguments
// document is refused, not ignored.
func TestWireTrailingDataInArgumentsIsRefused(t *testing.T) {
	h := newHarness(t)
	a := h.wire.call(t, a2a.NewTextPart(`{"skill":"promise_status","arguments":{"orderId":"ord-1001"}}`+"\n"+`{}`))
	wantProblem(t, a, 400, "malformed-request")
}

func skillPart(skill, argsJSON string) *a2a.Part {
	return a2a.NewDataPart(map[string]any{"skill": skill, "arguments": json.RawMessage(argsJSON)})
}

// ---- in-band problems: the SAME classification as the REST adapter -------------

func TestWireDomainRejectionsAreAnsweredInBand(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		name   string
		skill  string
		args   map[string]any
		status int
		slug   string
	}{
		{"unknown promise", "promise_status", map[string]any{"promiseId": "prm-nope"}, 404, "not-found"},
		{"order id that is not a valid id", "promise_status", map[string]any{"orderId": "ord 1001!"}, 422, "validation-failed"},
		{"node id that is not a valid id", "book_capacity", map[string]any{"nodeId": "N AUS3", "date": "2026-10-22"}, 422, "validation-failed"},
		{"date that is not YYYY-MM-DD", "book_capacity", map[string]any{"nodeId": "N-AUS3", "date": "22/10/2026"}, 422, "validation-failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := h.wire.skill(t, c.skill, c.args)
			wantProblem(t, a, c.status, c.slug)
			if a.env.Skill != c.skill || a.env.Problem.Detail == "" || a.env.Problem.Title == "" {
				t.Fatalf("problem answer = %s", a.raw)
			}
		})
	}
}

// failingReads makes every query fail with err.
type failingReads struct{ err error }

func (f failingReads) GetPromise(context.Context, string) (views.Promise, error) {
	return views.Promise{}, f.err
}

func (f failingReads) ListPromisesByOrder(context.Context, string) (views.PromiseList, error) {
	return views.PromiseList{}, f.err
}

func (f failingReads) ListPromiseBooks(context.Context, string, string) (views.BookList, error) {
	return views.BookList{}, f.err
}

// An unexpected failure is answered as a 500 problem WITHOUT its cause: a DSN
// or SQL text from the repository must never reach the calling agent.
func TestWireUnexpectedFailureIsA500WithoutTheCause(t *testing.T) {
	secret := "dial tcp postgres://admin:hunter2@10.0.0.9:5432/promise: connection refused"
	w := newWire(t, failingReads{err: errors.New(secret)})
	for _, c := range []struct {
		skill string
		args  map[string]any
	}{
		{"promise_status", map[string]any{"orderId": "ord-1"}},
		{"promise_status", map[string]any{"promiseId": "prm-1"}},
		{"book_capacity", map[string]any{"nodeId": "N-1", "date": "2026-10-22"}},
	} {
		a := w.skill(t, c.skill, c.args)
		wantProblem(t, a, 500, "internal-error")
		if strings.Contains(a.all(), "hunter2") || strings.Contains(a.all(), "postgres://") || strings.Contains(a.all(), "10.0.0.9") {
			t.Fatalf("the cause leaked into the answer: %s", a.all())
		}
	}
}

// Typed errors keep the REST classification even when wrapped by a use case.
func TestWireTypedErrorsKeepTheirRESTStatusWhenWrapped(t *testing.T) {
	notFound := newWire(t, failingReads{err: fmt.Errorf("load promise: %w", ports.ErrNotFound)})
	wantProblem(t, notFound.skill(t, "promise_status", map[string]any{"promiseId": "prm-1"}), 404, "not-found")
	invalid := newWire(t, failingReads{err: shared.Invalid("date %q is not YYYY-MM-DD", "x")})
	wantProblem(t, invalid.skill(t, "book_capacity", map[string]any{"nodeId": "N-1", "date": "x"}), 422, "validation-failed")
}

// ---- read-only is structural ------------------------------------------------------

// The adapter's only dependency is the Reads interface and it declares exactly
// the three queries the two skills drive; a write method fails the build of
// this test (the write gate: MCP offers no write, so neither does A2A).
func TestReadsDeclaresOnlyTheThreeQueries(t *testing.T) {
	typ := reflect.TypeFor[inbounda2a.Reads]()
	var got []string
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	want := []string{"GetPromise", "ListPromiseBooks", "ListPromisesByOrder"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reads methods = %v, want exactly %v", got, want)
	}
	deps := reflect.TypeFor[inbounda2a.Deps]()
	if deps.NumField() != 1 || deps.Field(0).Name != "Reads" {
		t.Fatalf("Deps must hold only Reads, has %d field(s)", deps.NumField())
	}
}

// Serving the skills changes no state: the in-memory outbox is untouched.
func TestWireSkillsWriteNothing(t *testing.T) {
	h := newHarness(t)
	before := h.store.Counts()
	if before.OutboxEvents == 0 || before.Promises == 0 {
		t.Fatalf("the seed must have written events and a promise, counts = %+v", before)
	}
	h.wire.skill(t, "promise_status", map[string]any{"orderId": "ord-1001"})
	h.wire.skill(t, "promise_status", map[string]any{"promiseId": h.promiseID})
	h.wire.skill(t, "promise_status", map[string]any{"promiseId": "prm-nope"})
	h.wire.skill(t, "book_capacity", map[string]any{"nodeId": "N-AUS3", "date": "2026-10-22"})
	h.wire.skill(t, "book_capacity", map[string]any{"nodeId": "N-AUS3", "date": "bad"})
	if after := h.store.Counts(); after != before {
		t.Fatalf("store counts %+v -> %+v: a read skill changed state", before, after)
	}
}

// ---- observability ---------------------------------------------------------------

// Every skill run is one span "a2a.skill <id>" carrying the a2a.skill
// attribute, like the MCP adapter's mcp.tool spans; a rejected request that
// never reaches a skill opens none.
func TestWireOneSpanPerSkillRun(t *testing.T) {
	previous := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))

	h := newHarness(t)
	h.wire.skill(t, "promise_status", map[string]any{"orderId": "ord-1001"})
	h.wire.skill(t, "book_capacity", map[string]any{"nodeId": "N-AUS3", "date": "2026-10-22"})
	h.wire.skill(t, "promise_status", map[string]any{"promiseId": "prm-nope"}) // a 404 answer
	h.wire.call(t, a2a.NewTextPart("not json"))                                // never reaches a skill

	var got []string
	for _, span := range recorder.Ended() {
		if !strings.HasPrefix(span.Name(), "a2a.skill ") {
			continue
		}
		attr := ""
		for _, kv := range span.Attributes() {
			if string(kv.Key) == "a2a.skill" {
				attr = kv.Value.AsString()
			}
		}
		if want := strings.TrimPrefix(span.Name(), "a2a.skill "); attr != want {
			t.Errorf("span %q carries a2a.skill=%q, want %q", span.Name(), attr, want)
		}
		got = append(got, span.Name())
	}
	want := []string{"a2a.skill promise_status", "a2a.skill book_capacity", "a2a.skill promise_status"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("skill spans = %v, want %v", got, want)
	}
}
