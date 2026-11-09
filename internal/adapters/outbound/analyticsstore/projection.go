package analyticsstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// Projection is the Postgres WRITER (report.Projection). Apply claims the
// event id and projects the fact in ONE transaction: the id is recorded if and
// only if its effect is, so a failure anywhere leaves nothing behind and the
// redelivered event is applied afresh, while a replay of an applied id is a
// no-op.
type Projection struct {
	pool *pgxpool.Pool
}

// NewProjection constructs a Projection over the writer pool.
func NewProjection(pool *pgxpool.Pool) *Projection { return &Projection{pool: pool} }

var _ report.Projection = (*Projection)(nil)

const claimSQL = `
	INSERT INTO analytics_processed_events (event_id, event_type, occurred_at)
	VALUES ($1, $2, $3)
	ON CONFLICT (event_id) DO NOTHING`

const (
	upsertOfferSQL = `
	INSERT INTO offers_quoted (offer_id, tier, quoted_at, event_id)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (offer_id) DO UPDATE
	SET tier = EXCLUDED.tier, quoted_at = EXCLUDED.quoted_at, event_id = EXCLUDED.event_id`

	upsertMadeSQL = `
	INSERT INTO promises_made (promise_id, offer_id, tier, basis, made_at, deliver_by_earliest, deliver_by_latest,
	                           time_zone, window_minutes, exact_day, zone_known, event_id)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	ON CONFLICT (promise_id) DO UPDATE
	SET offer_id = EXCLUDED.offer_id, tier = EXCLUDED.tier, basis = EXCLUDED.basis, made_at = EXCLUDED.made_at,
	    deliver_by_earliest = EXCLUDED.deliver_by_earliest, deliver_by_latest = EXCLUDED.deliver_by_latest,
	    time_zone = EXCLUDED.time_zone, window_minutes = EXCLUDED.window_minutes, exact_day = EXCLUDED.exact_day,
	    zone_known = EXCLUDED.zone_known, event_id = EXCLUDED.event_id`

	insertRevisionSQL = `
	INSERT INTO promise_revisions (promise_id, revision_no, revised_at, event_id)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (promise_id, revision_no) DO NOTHING`

	insertUnkeepableSQL = `
	INSERT INTO promise_unkeepable (promise_id, unkeepable_at, event_id)
	VALUES ($1, $2, $3)
	ON CONFLICT (promise_id) DO NOTHING`

	upsertJudgementSQL = `
	INSERT INTO promise_judgements (promise_id, verdict, cause, late_by_minutes, judged_at, event_id)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (promise_id) DO UPDATE
	SET verdict = EXCLUDED.verdict, cause = EXCLUDED.cause, late_by_minutes = EXCLUDED.late_by_minutes,
	    judged_at = EXCLUDED.judged_at, event_id = EXCLUDED.event_id`
)

// Apply implements report.Projection.
func (p *Projection) Apply(ctx context.Context, e report.Event) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("analyticsstore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	tag, err := tx.Exec(ctx, claimSQL, e.EventID, string(e.Kind), e.At)
	if err != nil {
		return false, classify(fmt.Errorf("analyticsstore: claim event %s: %w", e.EventID, err))
	}
	if tag.RowsAffected() == 0 {
		return false, nil // already applied: the deferred rollback ends the empty tx
	}
	if err := project(ctx, tx, e); err != nil {
		return false, classify(fmt.Errorf("analyticsstore: project %s %s: %w", e.Kind, e.EventID, err))
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("analyticsstore: commit: %w", err)
	}
	return true, nil
}

// errUnknownKind is deterministic: no retry can make the store accept it.
var errUnknownKind = errors.New("unknown event kind")

// statement is the SQL and arguments that project one fact.
func statement(e report.Event) (string, []any, error) {
	if e.Kind == report.KindDeliveryOfferQuoted {
		return upsertOfferSQL, []any{e.OfferID, e.Tier, e.At, e.EventID}, nil
	}
	if e.Kind == report.KindPromiseMade {
		w := report.WindowOf(e.DeliverByEarliest, e.DeliverByLatest, e.TimeZone)
		return upsertMadeSQL, []any{e.PromiseID, e.OfferID, e.Tier, e.Basis, e.At, e.DeliverByEarliest, e.DeliverByLatest,
			e.TimeZone, w.Minutes, w.ExactDay, w.ZoneKnown, e.EventID}, nil
	}
	if e.Kind == report.KindPromiseRevised {
		return insertRevisionSQL, []any{e.PromiseID, e.RevisionNo, e.At, e.EventID}, nil
	}
	if e.Kind == report.KindPromiseUnkeepable {
		return insertUnkeepableSQL, []any{e.PromiseID, e.At, e.EventID}, nil
	}
	if verdict, ok := report.VerdictOf(e.Kind); ok {
		return upsertJudgementSQL, []any{e.PromiseID, verdict, e.Cause, e.LateByMinutes, e.At, e.EventID}, nil
	}
	return "", nil, fmt.Errorf("%w %q", errUnknownKind, e.Kind)
}

func project(ctx context.Context, tx pgx.Tx, e report.Event) error {
	query, args, err := statement(e)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, query, args...)
	return err
}

// classify wraps a Postgres data-exception (SQLSTATE class 22) or
// integrity-violation (class 23) error, and an unknown event kind, in
// report.ErrRejected: the same event can never succeed, so the consumer
// dead-letters it instead of retrying forever. Everything else (connection
// loss, timeouts, locks, failovers, a raised exception) stays transient.
func classify(err error) error {
	var pgErr *pgconn.PgError
	if errors.Is(err, errUnknownKind) || (errors.As(err, &pgErr) && deterministicClass(pgErr.Code)) {
		return fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	return err
}

// deterministicClass reports whether an SQLSTATE is in class 22 or 23.
func deterministicClass(code string) bool {
	return len(code) == 5 && (code[:2] == "22" || code[:2] == "23")
}
