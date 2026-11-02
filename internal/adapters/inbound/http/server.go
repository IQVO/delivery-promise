// Package http is the inbound REST adapter (chi): it decodes requests, calls
// one use case per operation of apis/openapi.yaml and writes the answer or the
// RFC 9457 problem of the error. It holds no business rule.
package http

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/delivery-promise/internal/adapters/inbound/requestlog"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
)

// DefaultServiceName names the server spans, the HTTP metrics and the logs when
// the composition root passes no service name.
const DefaultServiceName = "delivery-promise"

// API is the set of use cases the REST adapter drives; *usecases.Service is the
// production implementation.
type API interface {
	QuoteDeliveryOffers(ctx context.Context, cmd usecases.QuoteCommand) (views.QuoteResult, error)
	GetOffer(ctx context.Context, id string) (views.Offer, error)
	MakePromise(ctx context.Context, cmd usecases.MakePromiseCommand) (usecases.MakePromiseResult, error)
	ListPromisesByOrder(ctx context.Context, orderID string) (views.PromiseList, error)
	GetPromise(ctx context.Context, id string) (views.Promise, error)
	ListPromiseBooks(ctx context.Context, nodeID, date string) (views.BookList, error)
	ConfirmPromise(ctx context.Context, id, orderID string, groupNo int) (views.Promise, error)
	CancelPromise(ctx context.Context, id string) (views.Promise, error)
	PublishPromisePolicy(ctx context.Context, in policy.Input) (views.Policy, error)
	CurrentPolicy(ctx context.Context) (views.Policy, error)
}

// Server holds the collaborators of the handlers.
type Server struct {
	API API
	// Readiness gates /readyz (nil = always ready, a unit-test fixture).
	Readiness *Readiness
	// Idempotency wraps the mutating routes that require an Idempotency-Key
	// (nil = no middleware, a unit-test fixture; cmd/api always sets it).
	Idempotency func(http.Handler) http.Handler
}

func (s *Server) idempotent(h http.HandlerFunc) http.Handler {
	if s.Idempotency == nil {
		return h
	}
	return s.Idempotency(h)
}

// NewRouter builds the chi router: /healthz and /readyz, then the operations of
// apis/openapi.yaml behind the NUL-rejecting middleware. A nil logger defaults
// to slog.Default(); an empty serviceName to DefaultServiceName.
//
// Middleware order is the fleet observability standard: RequestID, otelchi (the
// server span, named after the route pattern), the request-duration histogram,
// RequestLogger (inside the span, so its record carries trace_id and span_id),
// then Recoverer (this adapter's, which answers the internal-error problem).
// telemetry.Setup must have run before this call: otelchimetric binds the
// global MeterProvider when it is constructed.
func NewRouter(s *Server, logger *slog.Logger, serviceName string) http.Handler {
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(Recoverer)
	r.Use(rejectNUL)
	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	r.Method(http.MethodPost, "/delivery-quotes", s.idempotent(s.handleQuote))
	r.Get("/delivery-offers/{offerId}", s.handleGetOffer)
	r.Method(http.MethodPost, "/promises", s.idempotent(s.handleMakePromise))
	r.Get("/promises", s.handleListPromises)
	r.Get("/promises/{promiseId}", s.handleGetPromise)
	r.Method(http.MethodPost, "/promises/{promiseId}/confirmations", s.idempotent(s.handleConfirm))
	r.Method(http.MethodPost, "/promises/{promiseId}/cancellations", s.idempotent(s.handleCancel))
	r.Get("/promise-books", s.handleListBooks)
	r.Method(http.MethodPost, "/promise-policies", s.idempotent(s.handlePublishPolicy))
	r.Get("/promise-policies/current", s.handleCurrentPolicy)
	return r
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}
