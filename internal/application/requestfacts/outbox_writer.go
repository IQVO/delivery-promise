package requestfacts

import (
	"context"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/domain/requestfact"
)

// OutboxWriter is the production Writer: one analytics-only outbox row per
// fact, appended through the repo's own outbox writer in its OWN short
// transaction, never in the transaction of any request.
type OutboxWriter struct {
	UoW    ports.UnitOfWork
	Outbox ports.EventOutbox
	// NewID mints the requestId, the CloudEvents subject of the fact.
	NewID func() string
}

// Write implements Writer.
func (w OutboxWriter) Write(ctx context.Context, f requestfact.Fact) error {
	event := f.Event(w.NewID())
	return w.UoW.Do(ctx, func(ctx context.Context) error {
		return w.Outbox.Append(ctx, event)
	})
}
