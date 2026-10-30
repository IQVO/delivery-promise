// Package usecases is the application layer: one method per command and query
// of the model and per REST operation, plus the projections that keep the local
// read models and drive RepromiseOnRisk, JudgeOnDelivery and CancelPromise.
// Every use case that changes an aggregate saves it and appends its events to
// the outbox in ONE unit of work; a stale write (ports.ErrConcurrentUpdate) is
// retried up to MaxAttempts times (ADR 0002, A28).
package usecases

import (
	"context"
	"errors"
	"log/slog"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
)

// MaxAttempts is how many times a use case runs when it keeps losing the
// optimistic-concurrency race.
const MaxAttempts = 3

// Deps are the out ports a Service needs.
type Deps struct {
	UoW       ports.UnitOfWork
	Offers    ports.OfferRepository
	Promises  ports.PromiseRepository
	Books     ports.BookRepository
	Policies  ports.PolicyRepository
	Network   ports.NetworkViews
	Shipments ports.ShipmentViews
	Outbox    ports.EventOutbox
	Inbox     ports.Inbox
	Clock     ports.Clock
	IDs       ports.IDGenerator
	Logger    *slog.Logger
	// Metrics records the business outcome of each MakePromise; nil records
	// nothing, so unit tests need no wiring.
	Metrics ports.PromiseMetrics
}

// Service holds every use case of the context.
type Service struct {
	Deps
}

// New builds the Service. A nil Logger discards the log.
func New(d Deps) *Service {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Metrics == nil {
		d.Metrics = noMetrics{}
	}
	return &Service{Deps: d}
}

// tx runs fn in a unit of work, retrying a lost optimistic-concurrency race.
func (s *Service) tx(ctx context.Context, fn func(ctx context.Context) error) error {
	var err error
	for attempt := 0; attempt < MaxAttempts; attempt++ {
		if err = s.UoW.Do(ctx, fn); !errors.Is(err, ports.ErrConcurrentUpdate) {
			return err
		}
	}
	return err
}

// currentPolicy returns the highest published policy, or the built-in default.
func (s *Service) currentPolicy(ctx context.Context) (*policy.Policy, error) {
	p, err := s.Policies.Current(ctx)
	if errors.Is(err, ports.ErrNotFound) {
		return policy.Default(), nil
	}
	return p, err
}
