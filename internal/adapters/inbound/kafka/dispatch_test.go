package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/memory"
	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

var t0 = time.Date(2026, 10, 22, 19, 5, 0, 0, time.UTC)

// newService is a Service on the in-memory doubles; uow replaces the unit of
// work when a test needs a failing one.
func newService(uow ports.UnitOfWork) (*usecases.Service, *memory.Store) {
	store := memory.NewStore()
	if uow == nil {
		uow = store.UnitOfWork()
	}
	svc := usecases.New(usecases.Deps{
		UoW: uow, Offers: store.Offers(), Promises: store.Promises(), Books: store.Books(), Policies: store.Policies(),
		Network: store.Network(), Shipments: store.Shipments(), Outbox: store.Outbox(), Inbox: store.Inbox(),
		Clock: memory.NewFakeClock(t0), IDs: &memory.SeqIDs{},
	})
	return svc, store
}

// message builds a structured-mode CloudEvent for a foreign producer.
func message(t *testing.T, id, typ string, data any) []byte {
	t.Helper()
	e := ce.New()
	e.SetID(id)
	e.SetSource("/retailnetwork/test")
	e.SetType(typ)
	e.SetSubject("s")
	e.SetTime(t0)
	if err := e.SetData("application/json", data); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const nodeRegistered = "com.retailnetwork.logistics.node-topology.node.NodeRegistered"

func registered(id string) map[string]any {
	return map[string]any{
		"nodeId": id, "nodeType": "FULFILLMENT_CENTER", "subtype": nil, "operator": "VENDOR", "regionId": "R-SC",
		"timezone": "America/Chicago", "postalCode": "78701", "countryCode": "US", "someFutureField": 42,
	}
}

func TestADeliveredEventIsProjectedAndUnknownFieldsAreIgnored(t *testing.T) {
	svc, store := newService(nil)
	c := &Consumer{Name: "t", Handlers: Handlers(svc), Logger: discard()}
	if err := c.HandleMessage(context.Background(), message(t, "evt-1", nodeRegistered, registered("N-1"))); err != nil {
		t.Fatal(err)
	}
	n, err := store.Network().GetNode(context.Background(), "N-1")
	if err != nil || n.Type != "FULFILLMENT_CENTER" || n.RegionID != "R-SC" {
		t.Fatalf("node = %+v err=%v", n, err)
	}
}

func TestARedeliveryOfTheSameEventIdChangesNothing(t *testing.T) {
	svc, store := newService(nil)
	c := &Consumer{Name: "t", Handlers: Handlers(svc), Logger: discard()}
	msg := message(t, "evt-1", nodeRegistered, registered("N-1"))
	if err := c.HandleMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	before := store.Counts()
	if err := c.HandleMessage(context.Background(), msg); err != nil || store.Counts() != before {
		t.Fatalf("redelivery err=%v counts %+v -> %+v", err, before, store.Counts())
	}
}

func TestDeterministicProblemsAreSkippedNotRetried(t *testing.T) {
	svc, store := newService(nil)
	c := &Consumer{Name: "t", Handlers: Handlers(svc), Logger: discard()}
	cases := map[string][]byte{
		"not json":           []byte("{"),
		"flat legacy event":  []byte(`{"eventType":"NodeRegistered","nodeId":"N-1"}`),
		"unknown type":       message(t, "evt-2", "com.retailnetwork.logistics.node-topology.node.NodeTeleported", registered("N-2")),
		"foreign warehouse":  message(t, "evt-3", "com.warehouse.inventory.stock.StockMoved", map[string]any{"a": 1}),
		"missing identifier": message(t, "evt-4", nodeRegistered, registered("")),
	}
	for name, msg := range cases {
		if err := c.HandleMessage(context.Background(), msg); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if store.Counts() != (memory.Counts{}) {
		t.Fatalf("a skipped message changed the store: %+v", store.Counts())
	}
}

func TestAPayloadThatCanNeverBeAppliedIsPoisonWhenADeadLetterSinkExists(t *testing.T) {
	svc, _ := newService(nil)
	bad := message(t, "evt-5", nodeRegistered, registered(""))
	withDLQ := &Consumer{Name: "t", Handlers: Handlers(svc), DLQ: &fakeDLQ{}, Logger: discard()}
	if err := withDLQ.HandleMessage(context.Background(), bad); !errors.Is(err, errPoison) || !errors.Is(err, errBadPayload) {
		t.Fatalf("err = %v", err)
	}
	wrongShape := message(t, "evt-6", "com.retailnetwork.logistics.node-topology.lane.LaneOpened", map[string]any{"laneId": 7})
	if err := withDLQ.HandleMessage(context.Background(), wrongShape); !errors.Is(err, errPoison) {
		t.Fatalf("wrong shape err = %v", err)
	}
}

type failingUoW struct{ err error }

func (f failingUoW) Do(context.Context, func(context.Context) error) error { return f.err }

func TestATransientFailureIsReturnedForRetry(t *testing.T) {
	boom := errors.New("database down")
	svc, _ := newService(failingUoW{boom})
	c := &Consumer{Name: "t", Handlers: Handlers(svc), DLQ: &fakeDLQ{}, Logger: discard()}
	err := c.HandleMessage(context.Background(), message(t, "evt-7", nodeRegistered, registered("N-1")))
	if !errors.Is(err, boom) || errors.Is(err, errPoison) {
		t.Fatalf("err = %v", err)
	}
}

func TestDecodeDataChecksRequiredIdentifiers(t *testing.T) {
	e := ce.New()
	_ = e.SetData("application/json", map[string]any{"a": "x", "b": ""})
	type payload struct{ A, B string }
	if _, err := decodeData(e, func(p payload) []string { return ids(p.A) }); err != nil {
		t.Fatalf("A is present: %v", err)
	}
	if _, err := decodeData(e, func(p payload) []string { return ids(p.A, p.B) }); !errors.Is(err, errBadPayload) {
		t.Fatalf("B is empty: %v", err)
	}
	if _, err := decodeData[payload](e, nil); err != nil {
		t.Fatalf("no required ids: %v", err)
	}
	bad := ce.New()
	_ = bad.SetData("application/json", []int{1})
	if _, err := decodeData(bad, func(payload) []string { return nil }); !errors.Is(err, errBadPayload) {
		t.Fatalf("an array is not a payload: %v", err)
	}
}

// TestHandlersAreExactlyTheConsumedMessagesOfTheContract keeps the dispatch
// table and the `receive` side of apis/asyncapi.yaml in step: every handler
// type is declared as a consumed message and every consumed message has a
// handler.
func TestHandlersAreExactlyTheConsumedMessagesOfTheContract(t *testing.T) {
	spec, err := os.ReadFile("../../../../apis/asyncapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, m := range regexp.MustCompile(`com\.retailnetwork\.[a-z]+\.[a-z-]+\.[a-z]+\.[A-Za-z]+`).FindAllString(string(spec), -1) {
		if !strings.Contains(m, ".delivery-promise.") {
			declared[m] = true
		}
	}
	svc, _ := newService(nil)
	var missing, extra []string
	handlers := Handlers(svc)
	for typ := range handlers {
		if !declared[typ] {
			missing = append(missing, typ)
		}
	}
	for typ := range declared {
		if _, ok := handlers[typ]; !ok {
			extra = append(extra, typ)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("handlers not in asyncapi.yaml: %v\nconsumed in asyncapi.yaml without a handler: %v", missing, extra)
	}
	if len(handlers) != 34 {
		t.Fatalf("expected 34 consumed types, got %d", len(handlers))
	}
}

func TestTopicsAreTheEightIntegrationStreams(t *testing.T) {
	topics := Topics()
	if len(topics) != 8 {
		t.Fatalf("topics = %v", topics)
	}
	for _, topic := range topics {
		if !strings.HasPrefix(topic, "retailnetwork.") || !strings.HasSuffix(topic, ".events") {
			t.Errorf("topic %q is not an integration stream", topic)
		}
	}
}

func TestEveryProjectionHandlerAcceptsAMinimalPayload(t *testing.T) {
	svc, _ := newService(nil)
	c := &Consumer{Name: "t", Handlers: Handlers(svc), DLQ: &fakeDLQ{}, Logger: discard()}
	payloads := map[string]map[string]any{
		"logistics.node-topology.node.NodeActivated":                      {"nodeId": "N-1"},
		"logistics.node-topology.node.NodeSuspended":                      {"nodeId": "N-1"},
		"logistics.node-topology.node.NodeRetired":                        {"nodeId": "N-1"},
		"logistics.node-topology.region.RegionDefined":                    {"regionId": "R-1", "code": "SC", "name": "n"},
		"commerce.offer-availability.catalogitem.CatalogItemCreated":      {"catalogItemId": "ci-1"},
		"commerce.offer-availability.catalogitem.CatalogItemDiscontinued": {"catalogItemId": "ci-1"},
		"commerce.offer-availability.listing.ListingSuppressed":           {"listingId": "l-1", "nodeId": "N-1", "reason": "x"},
		"commerce.offer-availability.listing.ListingRestored":             {"listingId": "l-1", "nodeId": "N-1"},
		"logistics.linehaul-transportation.departure.DepartureCancelled":  {"departureId": "d-1"},
		"logistics.last-mile-delivery.wave.DeliveryWaveCancelled":         {"waveId": "w-1"},
		"logistics.shipping-and-tracking.journey.PackageLost":             {"trackingNumber": "T-1"},
		"logistics.shipping-and-tracking.journey.PackageReturned":         {"trackingNumber": "T-1"},
		"logistics.shipping-and-tracking.journey.PackageDelivered":        {"trackingNumber": "T-1", "deliveredAt": "2026-10-23T12:00:00Z"},
		"commerce.customer-ordering.customerorder.CustomerOrderCancelled": {"orderId": "ord-1", "groupNos": []int{}},
		"commerce.customer-ordering.customerorder.CustomerOrderPlaced":    {"orderId": "ord-1", "groups": []any{}},
	}
	i := 0
	for suffix, data := range payloads {
		i++
		typ := "com.retailnetwork." + suffix
		if _, ok := c.Handlers[typ]; !ok {
			t.Errorf("no handler for %s", typ)
			continue
		}
		if err := c.HandleMessage(context.Background(), message(t, "evt-"+suffix, typ, data)); err != nil {
			t.Errorf("%s: %v", suffix, err)
		}
	}
	if i != len(payloads) {
		t.Fatal("loop did not run")
	}
}
