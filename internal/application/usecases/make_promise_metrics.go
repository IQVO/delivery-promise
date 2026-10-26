package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// refusalReasons maps a MakePromise failure to the problem slug the REST
// adapter answers with (same vocabulary as apis/openapi.yaml), in match order.
var refusalReasons = []struct {
	err    error
	reason string
}{
	{ports.ErrNotFound, "not-found"},
	{book.ErrPromiseCapacityExhausted, "promise-capacity-exhausted"},
	{book.ErrBookClosed, "book-closed"},
	{offer.ErrOfferNotQuotable, "offer-not-quotable"},
	{promise.ErrGroupSpansNodes, "group-spans-nodes"},
	{promise.ErrInvalidPromiseTerms, "invalid-promise-terms"},
	{ports.ErrConcurrentUpdate, "concurrent-update"},
	{shared.ErrValidation, "validation-failed"},
}

// internalErrorReason is the reason of every failure that is not a refusal the
// caller can act on (a database error, a bug).
const internalErrorReason = "internal-error"

// noMetrics is the PromiseMetrics of a Deps without one.
type noMetrics struct{}

func (noMetrics) PromiseMade(context.Context)            {}
func (noMetrics) PromiseRefused(context.Context, string) {}

// recordMakePromise counts one MakePromise attempt: made when a new promise was
// created, refused with the reason of err on failure, nothing for a replay.
func (s *Service) recordMakePromise(ctx context.Context, res MakePromiseResult, err error) {
	switch {
	case err != nil:
		s.Metrics.PromiseRefused(ctx, refusalReason(err))
	case res.Created:
		s.Metrics.PromiseMade(ctx)
	}
}

func refusalReason(err error) string {
	for _, r := range refusalReasons {
		if errors.Is(err, r.err) {
			return r.reason
		}
	}
	return internalErrorReason
}
