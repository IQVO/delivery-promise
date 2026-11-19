package requestfacts_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/memory"
	"github.com/claudioed/delivery-promise/internal/application/requestfacts"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/requestfact"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

type sink struct{ facts []requestfact.Fact }

func (s *sink) Offer(f requestfact.Fact) { s.facts = append(s.facts, f) }

type panickySink struct{}

func (panickySink) Offer(requestfact.Fact) { panic("boom") }

type countMetrics struct{ dropped []string }

func (*countMetrics) FactWritten(context.Context) {}
func (m *countMetrics) FactDropped(_ context.Context, reason string) {
	m.dropped = append(m.dropped, reason)
}

var answerAt = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func recorder(s requestfacts.Offerer) *requestfacts.Recorder {
	return requestfacts.NewRecorder(s, nil, func() time.Time { return answerAt })
}

func offerWith(prefix string, latest time.Time) views.Offer {
	return views.Offer{DestinationPostalPrefix: prefix, Window: views.Window{Latest: latest, TimeZone: "America/Chicago"}}
}

func TestAQuoteWithOffersIsAnsweredWithItsShortestPromise(t *testing.T) {
	s := &sink{}
	recorder(s).RecordQuote("78701", 3, views.QuoteResult{Offers: []views.Offer{
		offerWith("78701", answerAt.AddDate(0, 0, 4)), offerWith("78701", answerAt.AddDate(0, 0, 2)),
	}})
	if len(s.facts) != 1 {
		t.Fatalf("facts = %+v", s.facts)
	}
	f := s.facts[0]
	if f.Kind != requestfact.KindQuote || f.Region != "78" || f.Items != 3 || !f.Answered || f.ShortestDays == nil || *f.ShortestDays != 2 || !f.At.Equal(answerAt) {
		t.Fatalf("fact = %+v", f)
	}
}

func TestAQuoteWithNoOfferIsAnAnswerWithoutAPromise(t *testing.T) {
	s := &sink{}
	recorder(s).RecordQuote("787", 1, views.QuoteResult{Offers: []views.Offer{}, Unavailable: []views.Unavailable{{Tier: "NEXT_DAY", Constraint: "DESTINATION_NOT_SERVED"}}})
	if len(s.facts) != 1 || s.facts[0].Answered || s.facts[0].ShortestDays != nil || s.facts[0].Region != "78" {
		t.Fatalf("facts = %+v", s.facts)
	}
}

func TestAPickupPointQuoteTakesItsRegionFromTheOffersOrLeavesNoFact(t *testing.T) {
	s := &sink{}
	r := recorder(s)
	r.RecordQuote("", 2, views.QuoteResult{Offers: []views.Offer{offerWith("1001", answerAt.AddDate(0, 0, 1))}})
	r.RecordQuote("", 2, views.QuoteResult{})
	if len(s.facts) != 1 || s.facts[0].Region != "10" {
		t.Fatalf("facts = %+v (a pickup quote with no offer has no region and no fact)", s.facts)
	}
}

func TestAnEstimateIsOneFactOverEveryItem(t *testing.T) {
	s := &sink{}
	res := views.EstimateResult{DestinationPostalPrefix: "787", Estimates: []views.Estimate{
		{CatalogItemID: "a", Options: []views.EstimateOption{{Window: views.EstimateWindow{End: answerAt.AddDate(0, 0, 5), TimeZone: "America/Chicago"}}}},
		{CatalogItemID: "b", Options: []views.EstimateOption{}},
		{CatalogItemID: "c", Options: []views.EstimateOption{{Window: views.EstimateWindow{End: answerAt.AddDate(0, 0, 3)}}, {Window: views.EstimateWindow{End: answerAt.AddDate(0, 0, 8)}}}},
	}}
	recorder(s).RecordEstimate(3, res)
	if len(s.facts) != 1 {
		t.Fatalf("facts = %+v", s.facts)
	}
	f := s.facts[0]
	if f.Kind != requestfact.KindEstimate || f.Region != "78" || f.Items != 3 || !f.Answered || *f.ShortestDays != 3 {
		t.Fatalf("fact = %+v", f)
	}
}

func TestAnEstimateWithNoOptionIsAnAnswerWithoutAPromise(t *testing.T) {
	s := &sink{}
	recorder(s).RecordEstimate(1, views.EstimateResult{DestinationPostalPrefix: "787", Estimates: []views.Estimate{{Options: []views.EstimateOption{}}}})
	if len(s.facts) != 1 || s.facts[0].Answered {
		t.Fatalf("facts = %+v", s.facts)
	}
}

func TestARequestTheFactCannotDescribeLeavesNothing(t *testing.T) {
	s := &sink{}
	r := recorder(s)
	r.RecordEstimate(0, views.EstimateResult{DestinationPostalPrefix: "787"})
	r.RecordEstimate(1, views.EstimateResult{})
	if len(s.facts) != 0 {
		t.Fatalf("facts = %+v", s.facts)
	}
}

func TestARecorderNeverPanicsAndCountsTheFact(t *testing.T) {
	m := &countMetrics{}
	r := requestfacts.NewRecorder(panickySink{}, m, func() time.Time { return answerAt })
	r.RecordEstimate(1, views.EstimateResult{DestinationPostalPrefix: "787"})
	r.RecordQuote("787", 1, views.QuoteResult{})
	if len(m.dropped) != 2 || m.dropped[0] != requestfacts.ReasonInternal {
		t.Fatalf("dropped = %v", m.dropped)
	}
	// and without metrics it still does not panic
	requestfacts.NewRecorder(panickySink{}, nil, func() time.Time { return answerAt }).RecordQuote("787", 1, views.QuoteResult{})
}

func TestTheOutboxWriterAppendsOneAnalyticsEventInItsOwnUnit(t *testing.T) {
	st := memory.NewStore()
	n := 0
	w := requestfacts.OutboxWriter{UoW: st.UnitOfWork(), Outbox: st.Outbox(), NewID: func() string { n++; return "req-" + string(rune('0'+n)) }}
	two := 2
	f := requestfact.Fact{Region: "78", Kind: requestfact.KindEstimate, Items: 4, Answered: true, ShortestDays: &two, At: answerAt}
	if err := w.Write(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	evs := st.Outbox().Events()
	if len(evs) != 2 || evs[0].SubjectID() != "req-1" || evs[1].SubjectID() != "req-2" || evs[0].EventName() != "DeliveryRequestAnswered" {
		t.Fatalf("outbox = %+v", evs)
	}
	got := evs[0].(requestfact.DeliveryRequestAnswered)
	if got.Region != "78" || got.Items != 4 || !got.Answered || *got.ShortestPromisedDays != 2 || !got.OccurredAt().Equal(answerAt) {
		t.Fatalf("event = %+v", got)
	}
}

type failingOutbox struct{ err error }

func (f failingOutbox) Append(context.Context, ...shared.Event) error { return f.err }

func TestAnOutboxFailureIsReturnedAndLeavesNoRow(t *testing.T) {
	st := memory.NewStore()
	boom := errors.New("outbox down")
	w := requestfacts.OutboxWriter{UoW: st.UnitOfWork(), Outbox: failingOutbox{boom}, NewID: func() string { return "req-1" }}
	if err := w.Write(context.Background(), requestfact.Fact{Region: "78", Kind: requestfact.KindQuote, Items: 1, At: answerAt}); !errors.Is(err, boom) {
		t.Fatalf("Write = %v", err)
	}
	if len(st.Outbox().Events()) != 0 {
		t.Fatal("a failed write left a row")
	}
}
