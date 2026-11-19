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

// catalogueEntry is one typed error, its problem and the replay class of that
// problem: what the Idempotency-Key middleware does with the answer (ADR 0031).
type catalogueEntry struct {
	err    error
	p      problem
	replay replayClass
}

// problemCatalogue maps every typed error to its problem, in match order. The
// slugs are the ones named by the responses of apis/openapi.yaml.
//
// Every entry declares its replay class (ADR 0031 holds the reason of each):
// FINAL when the answer cannot change for the same request, OUTCOME when the
// command committed state and then refused (none here: a refused request
// commits nothing), RETRYABLE when nothing was committed and the answer
// depends on state that can change (an optimistic-lock conflict, a fact an
// event may still deliver, capacity that is given back, a state the aggregate
// is not in yet). When unsure the class is RETRYABLE: the refusal rolled
// everything back, so running it again costs nothing. A new entry without a
// class fails TestEveryCatalogueEntryDeclaresAReplayClass.
var problemCatalogue = []catalogueEntry{
	// The request itself is wrong: a function of the request alone, FINAL.
	{errMalformedRequest, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}, replayFinal},
	{errUnknownQuery, problem{http.StatusBadRequest, "unknown-query-parameter", "Unknown query parameter"}, replayFinal},
	{page.ErrInvalidLimit, problem{http.StatusBadRequest, "invalid-limit", "Invalid limit"}, replayFinal},
	{page.ErrInvalidCursor, problem{http.StatusBadRequest, "invalid-cursor", "Invalid cursor"}, replayFinal},
	// The offer, promise or book named by the request. Ids are minted here and never deleted today, but one slug serves
	// three targets and a read-model port error, and nothing guards the invariant: RETRYABLE (ADR 0031, J1).
	{ports.ErrNotFound, problem{http.StatusNotFound, "not-found", "Not found"}, replayRetryable},
	// Capacity is given back, departures and waves are event-fed, a re-quote changes the offer under the same key: RETRYABLE.
	{book.ErrPromiseCapacityExhausted, problem{http.StatusConflict, "promise-capacity-exhausted", "Promise capacity exhausted"}, replayRetryable},
	{book.ErrBookClosed, problem{http.StatusConflict, "book-closed", "Promise book closed"}, replayRetryable},
	{offer.ErrOfferNotQuotable, problem{http.StatusConflict, "offer-not-quotable", "Offer is not quotable"}, replayRetryable},
	// Monotone and terminal for the same promise: FINAL. The next move is a new promise on a new path.
	{promise.ErrPromiseHoldExpired, problem{http.StatusConflict, "promise-hold-expired", "Promise hold expired"}, replayFinal},
	{promise.ErrPromiseClosed, problem{http.StatusConflict, "promise-closed", "Promise is closed"}, replayFinal},
	// Answered by the sweeper only, never by a keyed route; it needs a class and depends on the aggregate: RETRYABLE.
	{promise.ErrRevisionMovesEarlier, problem{http.StatusConflict, "revision-moves-earlier", "A revision may not move the promise earlier"}, replayRetryable},
	// The aggregate is not in the state the command needs yet, or the version lost the race: RETRYABLE.
	{shared.ErrIllegalTransition, problem{http.StatusConflict, "illegal-transition", "Illegal state transition"}, replayRetryable},
	{ports.ErrConcurrentUpdate, problem{http.StatusConflict, "concurrent-update", "The resource was modified by another request; re-fetch and retry"}, replayRetryable},
	{policy.ErrVersionNotNext, problem{http.StatusConflict, "concurrent-update", "Another policy was published at the same time; re-fetch and retry"}, replayRetryable},
	// Stock is an event-fed read model: RETRYABLE. The terms and tier rules read the stored offer, which never changes: FINAL.
	{promise.ErrGroupSpansNodes, problem{http.StatusUnprocessableEntity, "group-spans-nodes", "The group cannot ship from one node"}, replayRetryable},
	{promise.ErrInvalidPromiseTerms, problem{http.StatusUnprocessableEntity, "invalid-promise-terms", "Invalid promise terms"}, replayFinal},
	{promise.ErrNominalBasisNotAllowedForTier, problem{http.StatusUnprocessableEntity, "nominal-basis-not-allowed-for-tier", "A nominal basis is only allowed for the STANDARD tier"}, replayFinal},
	{offer.ErrNominalBasisNotAllowedForTier, problem{http.StatusUnprocessableEntity, "nominal-basis-not-allowed-for-tier", "A nominal basis is only allowed for the STANDARD tier"}, replayFinal},
	{shared.ErrValidation, problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}, replayFinal},
}

// problemFor returns the problem of the first catalogue entry err matches, or
// the internal-error problem.
func problemFor(err error) problem {
	if entry, ok := entryFor(err); ok {
		return entry.p
	}
	return internalProblem
}

// entryFor returns the first catalogue entry err matches.
func entryFor(err error) (catalogueEntry, bool) {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry, true
		}
	}
	return catalogueEntry{}, false
}

// replayOf returns the replay class of the first catalogue entry err matches;
// replayUnset for an error the catalogue does not know (an unmapped 5xx).
func replayOf(err error) replayClass {
	entry, _ := entryFor(err)
	return entry.replay
}
