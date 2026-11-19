package requestfacts

import (
	"context"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/requestfact"
)

// Offerer takes a fact without blocking: the Queue.
type Offerer interface {
	Offer(f requestfact.Fact)
}

// Recorder reduces the answer of a quote or an estimate to its fact and hands
// it over. It is what the REST adapter calls after the response was written:
// it never blocks, never returns an error and never panics, whatever the queue
// or the answer holds.
type Recorder struct {
	sink    Offerer
	metrics ports.RequestFactMetrics
	now     func() time.Time
}

// NewRecorder builds a Recorder that stamps facts with now (the answer
// instant) and offers them to sink. A nil metrics records nothing.
func NewRecorder(sink Offerer, m ports.RequestFactMetrics, now func() time.Time) *Recorder {
	return &Recorder{sink: sink, metrics: m, now: now}
}

// RecordQuote hands over the fact of a quote answered with res. The region is
// that of the request's destination prefix, or of the offers' when the request
// named a pickup point; a quote of a pickup point that got no offer has no
// region and leaves no fact.
func (r *Recorder) RecordQuote(destinationPostalPrefix string, items int, res views.QuoteResult) {
	options := make([]requestfact.Option, 0, len(res.Offers))
	prefix := destinationPostalPrefix
	for _, o := range res.Offers {
		options = append(options, requestfact.Option{Latest: o.Window.Latest, TimeZone: o.Window.TimeZone})
		if prefix == "" {
			prefix = o.DestinationPostalPrefix
		}
	}
	r.record(requestfact.KindQuote, prefix, items, options)
}

// RecordEstimate hands over the fact of an estimate answered with res, over
// the options of every item.
func (r *Recorder) RecordEstimate(items int, res views.EstimateResult) {
	var options []requestfact.Option
	for _, e := range res.Estimates {
		for _, o := range e.Options {
			options = append(options, requestfact.Option{Latest: o.Window.End, TimeZone: o.Window.TimeZone})
		}
	}
	r.record(requestfact.KindEstimate, res.DestinationPostalPrefix, items, options)
}

func (r *Recorder) record(kind requestfact.Kind, prefix string, items int, options []requestfact.Option) {
	defer func() {
		if recover() != nil && r.metrics != nil {
			r.metrics.FactDropped(context.Background(), ReasonInternal)
		}
	}()
	if f, ok := requestfact.New(kind, prefix, items, options, r.now()); ok {
		r.sink.Offer(f)
	}
}
