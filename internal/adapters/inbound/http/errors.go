package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/delivery-promise/internal/adapters/inbound/http/page"
	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// problemBaseURI is the namespace of this service's RFC 9457 `type` URIs
// (apis/openapi.yaml, Problem).
const problemBaseURI = "https://retailnetwork.example/problems/"

// errMalformedRequest marks a body or parameter the adapter itself rejects
// (bad JSON, unknown field, wrong type).
var errMalformedRequest = errors.New("request body is not valid JSON for this operation")

// problem is the fixed (status, slug, title) of one error category; the detail
// comes from the error at write time.
type problem struct {
	status int
	slug   string
	title  string
}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

// errUnknownQuery marks a collection GET that sent a query parameter it does not declare.
var errUnknownQuery = errors.New("unknown query parameter")

// Problems the middleware writes itself, before any use case runs.
var (
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required"}
	idempotencyKeyInvalid  = problem{http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is invalid"}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request"}
)

// problemCatalogue maps every typed error to its problem, in match order. The
// slugs are the ones named by the responses of apis/openapi.yaml.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}},
	{errUnknownQuery, problem{http.StatusBadRequest, "unknown-query-parameter", "Unknown query parameter"}},
	{page.ErrInvalidLimit, problem{http.StatusBadRequest, "invalid-limit", "Invalid limit"}},
	{page.ErrInvalidCursor, problem{http.StatusBadRequest, "invalid-cursor", "Invalid cursor"}},
	{ports.ErrNotFound, problem{http.StatusNotFound, "not-found", "Not found"}},
	{book.ErrPromiseCapacityExhausted, problem{http.StatusConflict, "promise-capacity-exhausted", "Promise capacity exhausted"}},
	{book.ErrBookClosed, problem{http.StatusConflict, "book-closed", "Promise book closed"}},
	{offer.ErrOfferNotQuotable, problem{http.StatusConflict, "offer-not-quotable", "Offer is not quotable"}},
	{promise.ErrPromiseHoldExpired, problem{http.StatusConflict, "promise-hold-expired", "Promise hold expired"}},
	{promise.ErrPromiseClosed, problem{http.StatusConflict, "promise-closed", "Promise is closed"}},
	{promise.ErrRevisionMovesEarlier, problem{http.StatusConflict, "revision-moves-earlier", "A revision may not move the promise earlier"}},
	{shared.ErrIllegalTransition, problem{http.StatusConflict, "illegal-transition", "Illegal state transition"}},
	{ports.ErrConcurrentUpdate, problem{http.StatusConflict, "concurrent-update", "The resource was modified by another request; re-fetch and retry"}},
	{policy.ErrVersionNotNext, problem{http.StatusConflict, "concurrent-update", "Another policy was published at the same time; re-fetch and retry"}},
	{promise.ErrGroupSpansNodes, problem{http.StatusUnprocessableEntity, "group-spans-nodes", "The group cannot ship from one node"}},
	{promise.ErrInvalidPromiseTerms, problem{http.StatusUnprocessableEntity, "invalid-promise-terms", "Invalid promise terms"}},
	{promise.ErrNominalBasisNotAllowedForTier, problem{http.StatusUnprocessableEntity, "nominal-basis-not-allowed-for-tier", "A nominal basis is only allowed for the STANDARD tier"}},
	{offer.ErrNominalBasisNotAllowedForTier, problem{http.StatusUnprocessableEntity, "nominal-basis-not-allowed-for-tier", "A nominal basis is only allowed for the STANDARD tier"}},
	{shared.ErrValidation, problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}},
}

// problemFor returns the problem of the first catalogue entry err matches, or
// the internal-error problem.
func problemFor(err error) problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
