package kafka

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/delivery-promise/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/outbox"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var (
	t0   = time.Date(2026, 10, 22, 19, 5, 0, 0, time.UTC)
	cdt  = time.FixedZone("CDT", -5*3600) // the instants below are written in local time on purpose
	hdr  = shared.Header{Version: 3, At: t0}
	wit  = shared.Witness{NodeID: "N-AUS3", PickupDepartureID: "dep-AUS3-1800", Legs: []string{"dep-SAU1-DAU1-2200"}, DeliveryWaveID: "W-DAU1-1023-A"}
	win  = shared.Window{Earliest: time.Date(2026, 10, 23, 9, 0, 0, 0, cdt), Latest: time.Date(2026, 10, 23, 21, 0, 0, 0, cdt)}
	term = promise.Terms{Tier: shared.TierNextDay, ShipBy: time.Date(2026, 10, 22, 18, 0, 0, 0, cdt), Window: win, Basis: shared.BasisScheduled, PolicyVersion: "v2"}
)

func goldenCases() map[string]shared.Event {
	nominal := term
	nominal.Basis, nominal.Tier = shared.BasisNominal, shared.TierStandard
	return map[string]shared.Event{
		"PromiseMade": promise.PromiseMade{Header: hdr, PromiseID: "prm-1", OfferID: "off-1", Terms: term, Witness: wit,
			HoldExpiresAt: t0.Add(15 * time.Minute), Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}}},
		"PromiseMade_nominal": promise.PromiseMade{Header: hdr, PromiseID: "prm-2", OfferID: "off-2", Terms: nominal, Witness: shared.Witness{NodeID: "N-AUS3"},
			HoldExpiresAt: t0.Add(15 * time.Minute), Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 1}}},
		"PromiseConfirmed": promise.PromiseConfirmed{Header: hdr, PromiseID: "prm-1", OrderID: "ord-1", GroupNo: 1},
		"PromiseAtRisk":    promise.PromiseAtRisk{Header: hdr, PromiseID: "prm-1", OrderID: "ord-1", Reason: "DEPARTURE_CANCELLED", DetectedAt: t0},
		"PromiseRevised": promise.PromiseRevised{Header: hdr, PromiseID: "prm-1", OrderID: "ord-1", RevisionNo: 1, NodeID: "N-AUS3",
			PreviousDeliverByLatest: win.Latest, Terms: term, Reason: "DEPARTURE_CANCELLED"},
		"PromiseKept": promise.PromiseKept{Header: hdr, PromiseID: "prm-1", OrderID: "ord-1", NodeID: "N-AUS3",
			DeliveredAt: time.Date(2026, 10, 23, 15, 0, 0, 0, cdt), DeliverByLatest: win.Latest},
		"PromiseBroken_late": promise.PromiseBroken{Header: hdr, PromiseID: "prm-1", OrderID: "ord-1", NodeID: "N-AUS3",
			DeliveredAt: time.Date(2026, 10, 23, 22, 0, 0, 0, cdt), DeliverByLatest: win.Latest, LateByMinutes: 60, Cause: promise.CauseLate},
		"PromiseBroken_lost": promise.PromiseBroken{Header: hdr, PromiseID: "prm-1", OrderID: "ord-1", NodeID: "N-AUS3",
			DeliverByLatest: win.Latest, Cause: promise.CauseLost},
		"PromiseReleased":   promise.PromiseReleased{Header: hdr, PromiseID: "prm-1", Reason: "HOLD_EXPIRED"},
		"PromiseCancelled":  promise.PromiseCancelled{Header: hdr, PromiseID: "prm-1", OrderID: "ord-1", Reason: "CUSTOMER_REQUEST"},
		"PromiseUnkeepable": promise.PromiseUnkeepable{Header: hdr, PromiseID: "prm-1", OrderID: "ord-1", GroupNo: 1, Reason: "NO_FEASIBLE_PATH"},
		"PromiseBookExhausted": book.PromiseBookExhausted{Header: hdr, BookID: "N-AUS3:dep-AUS3-1800", NodeID: "N-AUS3",
			PickupDepartureID: "dep-AUS3-1800", Share: 2500},
		"DeliveryOfferQuoted": offer.DeliveryOfferQuoted{Header: hdr, OfferID: "off-1", Tier: shared.TierNextDay, DestinationPrefix: "787", NodeID: "N-AUS3",
			Window: win, Basis: shared.BasisScheduled, PolicyVersion: "v2", ExpiresAt: t0.Add(10 * time.Minute)},
		"PromisePolicyPublished": policy.PromisePolicyPublished{Header: hdr, PolicyVersion: "v2", Number: 2, OfferTTLMinutes: 10, HoldTTLMinutes: 15,
			PackagesPerOrder: 2, SharePercent: 80, PublishedAt: t0,
			Deadlines: []policy.Deadline{{Tier: shared.TierNextDay, RegionID: "R-SC", Minute: 23*60 + 59}, {Tier: shared.TierSameDay, Minute: 9*60 + 5}}},
	}
}

func fixedEncoder() *Encoder {
	return NewEncoderWithIDs(func() string { return "7c1d3e52-0b6a-4f0e-9a41-5d2e8f3a0001" })
}

func TestEveryPublishedEventHasAnExactGoldenCloudEvent(t *testing.T) {
	for name, ev := range goldenCases() {
		t.Run(name, func(t *testing.T) {
			m, err := fixedEncoder().Encode(ev)
			if err != nil {
				t.Fatal(err)
			}
			got := indented(t, m.Value)
			path := filepath.Join("testdata", name+".golden.json")
			if *update {
				writeGolden(t, path, got)
			}
			want, err := os.ReadFile(path) //nolint:gosec // a fixed testdata path
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("CloudEvent differs from %s:\n got %s\nwant %s", path, got, want)
			}
		})
	}
}

func indented(t *testing.T, raw []byte) []byte {
	t.Helper()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	return pretty.Bytes()
}

func writeGolden(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll("testdata", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEveryEventIsRoutedToItsStreamTopicKeyAndHeader(t *testing.T) {
	for name, ev := range goldenCases() {
		m, err := fixedEncoder().Encode(ev)
		if err != nil {
			t.Fatal(err)
		}
		stream := streamOf[ev.EventName()]
		wantTopic := "retailnetwork.delivery-promise." + stream
		if m.Topic != wantTopic {
			t.Errorf("%s: topic %s, want %s", name, m.Topic, wantTopic)
		}
		if string(m.Key) != ev.SubjectID() || m.Subject != ev.SubjectID() {
			t.Errorf("%s: key %q subject %q, want %q", name, m.Key, m.Subject, ev.SubjectID())
		}
		if !strings.HasPrefix(m.EventType, "com.retailnetwork.commerce.delivery-promise.") || !strings.HasSuffix(m.EventType, "."+ev.EventName()) {
			t.Errorf("%s: type %s", name, m.EventType)
		}
		if len(m.Headers) != 1 || m.Headers[0].Key != "content-type" || m.Headers[0].Value != cloudevents.MediaType {
			t.Errorf("%s: headers %+v", name, m.Headers)
		}
		if m.EventID != "7c1d3e52-0b6a-4f0e-9a41-5d2e8f3a0001" {
			t.Errorf("%s: id %s", name, m.EventID)
		}
		if _, err := cloudevents.Decode(m.Value); err != nil {
			t.Errorf("%s: not a CloudEvent: %v", name, err)
		}
	}
}

func TestOnlyPublishedEventsEncode(t *testing.T) {
	if _, err := NewEncoder().Encode(unknownEvent{}); err == nil {
		t.Fatal("an unknown event encoded")
	}
	if _, err := NewEncoder().EncodeAll([]shared.Event{promise.PromiseReleased{Header: hdr, PromiseID: "prm-1"}, unknownEvent{}}); err == nil {
		t.Fatal("EncodeAll must fail on an unknown event")
	}
	// a published name with a payload mapping missing is a programming error that must surface
	if _, err := payload(unknownEvent{}); err == nil {
		t.Fatal("payload of an unknown event")
	}
	if _, err := NewEncoder().Encode(promise.PromiseReleased{Header: hdr}); err == nil {
		t.Fatal("an event without a subject must not encode")
	}
}

func TestEncodeAllKeepsOrderAndMintsDistinctIds(t *testing.T) {
	ms, err := NewEncoder().EncodeAll([]shared.Event{
		promise.PromiseReleased{Header: hdr, PromiseID: "prm-1", Reason: "A"}, promise.PromiseReleased{Header: hdr, PromiseID: "prm-2", Reason: "B"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Subject != "prm-1" || ms[1].Subject != "prm-2" || ms[0].EventID == ms[1].EventID {
		t.Fatalf("messages = %+v", ms)
	}
}

type unknownEvent struct{ shared.Header }

func (unknownEvent) EventName() string { return "Mystery" }
func (unknownEvent) Entity() string    { return "promise" }
func (unknownEvent) SubjectID() string { return "x" }

type fakeWriter struct {
	got []kafkago.Message
	err error
}

func (f *fakeWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	f.got = append(f.got, msgs...)
	return f.err
}

func TestRelaySinkWritesTopicKeyValueAndHeaders(t *testing.T) {
	w := &fakeWriter{}
	s := NewRelaySink(w)
	m, _ := fixedEncoder().Encode(promise.PromiseReleased{Header: hdr, PromiseID: "prm-1", Reason: "HOLD_EXPIRED"})
	if err := s.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if len(w.got) != 1 || w.got[0].Topic != TopicAnalytics || string(w.got[0].Key) != "prm-1" || !bytes.Equal(w.got[0].Value, m.Value) ||
		len(w.got[0].Headers) != 1 || w.got[0].Headers[0].Key != "content-type" {
		t.Fatalf("written = %+v", w.got)
	}
	if err := s.Send(context.Background()); err != nil || len(w.got) != 1 {
		t.Fatalf("an empty send must write nothing: %v", err)
	}
}

func TestRelaySinkRefusesATopiclessMessageAndWrapsWriteErrors(t *testing.T) {
	w := &fakeWriter{err: errors.New("broker down")}
	s := NewRelaySink(w)
	if err := s.Send(context.Background(), outbox.Message{EventType: "x"}); err == nil || len(w.got) != 0 {
		t.Fatalf("topic-less message: %v", err)
	}
	err := s.Send(context.Background(), outbox.Message{Topic: "t", EventType: "x"})
	if err == nil || !errors.Is(err, w.err) {
		t.Fatalf("err = %v", err)
	}
}
