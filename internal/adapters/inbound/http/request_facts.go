package http

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/claudioed/delivery-promise/internal/application/views"
)

// FactRecorder takes the best-effort request fact of an answered quote or
// estimate (ADR 0031). Both methods return at once and never fail: the request
// path never waits on, fails on or is slowed by the fact. *requestfacts.Recorder
// is the production implementation; a nil Server.Facts disables the feature.
type FactRecorder interface {
	RecordQuote(destinationPostalPrefix string, items int, res views.QuoteResult)
	RecordEstimate(items int, res views.EstimateResult)
}

type pendingFactKey struct{}

// pendingFact is what a handler leaves behind for recordsFacts: the hand-over
// to run once the whole response was written.
type pendingFact struct{ handOver func() }

// deferFact lets a handler ask for its fact to be handed over after the
// response; it does nothing when the feature is off (no recordsFacts wrapper).
func deferFact(r *http.Request, handOver func()) {
	if p, ok := r.Context().Value(pendingFactKey{}).(*pendingFact); ok {
		p.handOver = handOver
	}
}

// recordsFacts wraps a route so that the fact its handler asked for is handed
// over only after the handler chain returned (so after the idempotency
// transaction committed and the body was written) and only for a 200: a
// replayed key, a refusal or a failure leaves no fact. With the feature off
// (nil Facts) it returns next itself: the route is byte for byte the one
// without the feature.
func (s *Server) recordsFacts(next http.Handler) http.Handler {
	if s.Facts == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pending := &pendingFact{}
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r.WithContext(context.WithValue(r.Context(), pendingFactKey{}, pending)))
		if pending.handOver != nil && ww.Status() == http.StatusOK {
			handOver(pending.handOver)
		}
	})
}

// handOver runs the hand-over of a fact that was already answered: whatever it
// does, even a panic, cannot change the response that is gone.
func handOver(fn func()) {
	defer func() { _ = recover() }()
	fn()
}
