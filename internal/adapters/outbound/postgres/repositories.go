package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/repository"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
)

const uniqueViolation = "23505"

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == uniqueViolation
}

// scanSnapshot reads one jsonb snapshot column into v; a missing row is
// ports.ErrNotFound.
func scanSnapshot(row pgx.Row, v any) error {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ports.ErrNotFound
		}
		return err
	}
	return json.Unmarshal(raw, v)
}

// listIDs runs a query that returns one text id per row.
func listIDs[T ~string](ctx context.Context, q querier, sql string, args ...any) ([]T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, T(id))
	}
	return out, rows.Err()
}

// save applies the optimistic-concurrency rule of the ports package: version 1
// inserts (a duplicate key is ports.ErrConcurrentUpdate), a later version
// updates the row at the previous version (no row is ports.ErrConcurrentUpdate).
func save(ctx context.Context, q querier, version int64, insert, update string, insertArgs, updateArgs []any) error {
	if version == 1 {
		if _, err := q.Exec(ctx, insert, insertArgs...); err != nil {
			if isUnique(err) {
				return ports.ErrConcurrentUpdate
			}
			return err
		}
		return nil
	}
	tag, err := q.Exec(ctx, update, updateArgs...)
	if err != nil {
		if isUnique(err) {
			return ports.ErrConcurrentUpdate
		}
		return err
	}
	if tag.RowsAffected() != 1 {
		return ports.ErrConcurrentUpdate
	}
	return nil
}

// ---- offers -------------------------------------------------------------------------------------------------

// OfferRepository is the Postgres ports.OfferRepository.
type OfferRepository struct{ db }

// NewOfferRepository constructs an OfferRepository over pool.
func NewOfferRepository(pool *pgxpool.Pool) *OfferRepository { return &OfferRepository{db{pool}} }

// Get returns the offer or ports.ErrNotFound.
func (r *OfferRepository) Get(ctx context.Context, id offer.ID) (*offer.Offer, error) {
	var s offer.Snapshot
	if err := scanSnapshot(r.q(ctx).QueryRow(ctx, `SELECT snapshot FROM delivery_offers WHERE id = $1`, string(id)), &s); err != nil {
		return nil, err
	}
	return offer.Restore(s)
}

// ListExpired returns QUOTED offers with expires_at <= now, oldest first.
func (r *OfferRepository) ListExpired(ctx context.Context, now time.Time, limit int) ([]offer.ID, error) {
	return listIDs[offer.ID](ctx, r.q(ctx), `SELECT id FROM delivery_offers WHERE state = 'QUOTED' AND expires_at <= $1 ORDER BY expires_at, id LIMIT $2`, now, limit)
}

// Save stores the offer under the version rule.
func (r *OfferRepository) Save(ctx context.Context, o *offer.Offer) error {
	s := o.Snapshot()
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return save(ctx, r.q(ctx), s.Version,
		`INSERT INTO delivery_offers (id, state, expires_at, version, snapshot) VALUES ($1, $2, $3, $4, $5)`,
		`UPDATE delivery_offers SET state = $2, expires_at = $3, version = $4, snapshot = $5 WHERE id = $1 AND version = $4 - 1`,
		[]any{string(s.ID), string(s.State), s.ExpiresAt, s.Version, raw},
		[]any{string(s.ID), string(s.State), s.ExpiresAt, s.Version, raw})
}

// ---- promises -----------------------------------------------------------------------------------------------

// PromiseRepository is the Postgres ports.PromiseRepository.
type PromiseRepository struct{ db }

// NewPromiseRepository constructs a PromiseRepository over pool.
func NewPromiseRepository(pool *pgxpool.Pool) *PromiseRepository { return &PromiseRepository{db{pool}} }

const promiseCols = `snapshot`

func (r *PromiseRepository) one(ctx context.Context, where string, args ...any) (*promise.Promise, error) {
	var s promise.Snapshot
	if err := scanSnapshot(r.q(ctx).QueryRow(ctx, `SELECT `+promiseCols+` FROM promises WHERE `+where+` ORDER BY id LIMIT 1`, args...), &s); err != nil {
		return nil, err
	}
	return promise.Restore(s)
}

// Get returns the promise or ports.ErrNotFound.
func (r *PromiseRepository) Get(ctx context.Context, id promise.ID) (*promise.Promise, error) {
	return r.one(ctx, `id = $1`, string(id))
}

// GetByOffer returns the promise made from the offer or ports.ErrNotFound.
func (r *PromiseRepository) GetByOffer(ctx context.Context, offerID string) (*promise.Promise, error) {
	return r.one(ctx, `offer_id = $1`, offerID)
}

// FindByOrderGroup returns the live promise of an order group or ports.ErrNotFound.
func (r *PromiseRepository) FindByOrderGroup(ctx context.Context, orderID string, groupNo int) (*promise.Promise, error) {
	return r.one(ctx, `order_id = $1 AND group_no = $2`, orderID, groupNo)
}

// ListByOrder returns the order's promises by groupNo.
func (r *PromiseRepository) ListByOrder(ctx context.Context, orderID string) ([]*promise.Promise, error) {
	rows, err := r.q(ctx).Query(ctx, `SELECT snapshot FROM promises WHERE order_id = $1 ORDER BY group_no, id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*promise.Promise
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var s promise.Snapshot
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		p, err := promise.Restore(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListHoldsExpired returns PROVISIONAL promises with hold_expires_at <= now.
func (r *PromiseRepository) ListHoldsExpired(ctx context.Context, now time.Time, limit int) ([]promise.ID, error) {
	return listIDs[promise.ID](ctx, r.q(ctx), `SELECT id FROM promises WHERE state = 'PROVISIONAL' AND hold_expires_at <= $1 ORDER BY hold_expires_at, id LIMIT $2`, now, limit)
}

// ListAtRisk returns AT_RISK promises, oldest first.
func (r *PromiseRepository) ListAtRisk(ctx context.Context, limit int) ([]promise.ID, error) {
	return listIDs[promise.ID](ctx, r.q(ctx), `SELECT id FROM promises WHERE state = 'AT_RISK' ORDER BY made_at, id LIMIT $1`, limit)
}

// ListActiveByPath returns CONFIRMED and AT_RISK promises whose witness uses
// the departure (as pickup or leg) or the wave.
func (r *PromiseRepository) ListActiveByPath(ctx context.Context, f repository.PromiseRiskFilter) ([]promise.ID, error) {
	return listIDs[promise.ID](ctx, r.q(ctx), `
		SELECT id FROM promises
		WHERE state IN ('CONFIRMED', 'AT_RISK')
		  AND (($1 <> '' AND (pickup_departure_id = $1 OR $1 = ANY (leg_ids))) OR ($2 <> '' AND wave_id = $2))
		ORDER BY id`, f.DepartureID, f.WaveID)
}

// HeldQuantities sums the lines of PROVISIONAL promises per (node, item).
func (r *PromiseRepository) HeldQuantities(ctx context.Context, nodeIDs []string) (map[repository.HoldKey]int, error) {
	out := map[repository.HoldKey]int{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	rows, err := r.q(ctx).Query(ctx, `
		SELECT p.node_id, l->>'CatalogItemID', SUM((l->>'Quantity')::int)
		FROM promises p, jsonb_array_elements(p.snapshot->'Lines') AS l
		WHERE p.state = 'PROVISIONAL' AND p.node_id = ANY ($1)
		GROUP BY p.node_id, l->>'CatalogItemID'`, nodeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var node, item string
		var qty int
		if err := rows.Scan(&node, &item, &qty); err != nil {
			return nil, err
		}
		out[repository.HoldKey{NodeID: node, ItemID: item}] = qty
	}
	return out, rows.Err()
}

// Save stores the promise under the version rule; a second promise for the
// same offer or order group is ports.ErrConcurrentUpdate.
func (r *PromiseRepository) Save(ctx context.Context, p *promise.Promise) error {
	s := p.Snapshot()
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	legs := s.Witness.Legs
	if legs == nil {
		legs = []string{}
	}
	args := []any{
		string(s.ID), s.OfferID, s.OrderID, s.GroupNo, string(s.State), s.HoldExpiresAt, s.MadeAt, s.Witness.NodeID,
		s.Witness.PickupDepartureID, legs, s.Witness.DeliveryWaveID, s.Version, raw,
	}
	return save(ctx, r.q(ctx), s.Version,
		`INSERT INTO promises (id, offer_id, order_id, group_no, state, hold_expires_at, made_at, node_id, pickup_departure_id, leg_ids, wave_id, version, snapshot)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		`UPDATE promises SET offer_id = $2, order_id = $3, group_no = $4, state = $5, hold_expires_at = $6, made_at = $7, node_id = $8,
		        pickup_departure_id = $9, leg_ids = $10, wave_id = $11, version = $12, snapshot = $13
		 WHERE id = $1 AND version = $12 - 1`, args, args)
}

// ---- books --------------------------------------------------------------------------------------------------

// BookRepository is the Postgres ports.BookRepository.
type BookRepository struct{ db }

// NewBookRepository constructs a BookRepository over pool.
func NewBookRepository(pool *pgxpool.Pool) *BookRepository { return &BookRepository{db{pool}} }

// Get returns the book or ports.ErrNotFound.
func (r *BookRepository) Get(ctx context.Context, id book.ID) (*book.Book, error) {
	var s book.Snapshot
	if err := scanSnapshot(r.q(ctx).QueryRow(ctx, `SELECT snapshot FROM promise_books WHERE id = $1`, string(id)), &s); err != nil {
		return nil, err
	}
	return book.Restore(s)
}

// List returns the matching books by cutoffAt then id.
func (r *BookRepository) List(ctx context.Context, f repository.BookFilter) ([]*book.Book, error) {
	rows, err := r.q(ctx).Query(ctx, `
		SELECT snapshot FROM promise_books
		WHERE (coalesce(cardinality($1::text[]), 0) = 0 OR node_id = ANY ($1))
		  AND ($2::timestamptz IS NULL OR cutoff_at >= $2)
		  AND ($3::timestamptz IS NULL OR cutoff_at < $3)
		ORDER BY cutoff_at, id`, f.NodeIDs, nullTime(f.CutoffFrom), nullTime(f.CutoffUntil))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*book.Book
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var s book.Snapshot
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		b, err := book.Restore(s)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// ListOpenDue returns OPEN books with cutoff_at <= now, oldest first.
func (r *BookRepository) ListOpenDue(ctx context.Context, now time.Time, limit int) ([]book.ID, error) {
	return listIDs[book.ID](ctx, r.q(ctx), `SELECT id FROM promise_books WHERE state = 'OPEN' AND cutoff_at <= $1 ORDER BY cutoff_at, id LIMIT $2`, now, limit)
}

// Save stores the book under the version rule.
func (r *BookRepository) Save(ctx context.Context, b *book.Book) error {
	s := b.Snapshot()
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	args := []any{string(s.ID), s.NodeID, s.PickupDepartureID, s.CutoffAt, string(s.State), s.Version, raw}
	return save(ctx, r.q(ctx), s.Version,
		`INSERT INTO promise_books (id, node_id, pickup_departure_id, cutoff_at, state, version, snapshot) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		`UPDATE promise_books SET node_id = $2, pickup_departure_id = $3, cutoff_at = $4, state = $5, version = $6, snapshot = $7 WHERE id = $1 AND version = $6 - 1`,
		args, args)
}

// ---- policy -------------------------------------------------------------------------------------------------

// PolicyRepository is the Postgres ports.PolicyRepository.
type PolicyRepository struct{ db }

// NewPolicyRepository constructs a PolicyRepository over pool.
func NewPolicyRepository(pool *pgxpool.Pool) *PolicyRepository { return &PolicyRepository{db{pool}} }

// Current returns the highest version or ports.ErrNotFound.
func (r *PolicyRepository) Current(ctx context.Context) (*policy.Policy, error) {
	var s policy.Snapshot
	if err := scanSnapshot(r.q(ctx).QueryRow(ctx, `SELECT snapshot FROM promise_policies ORDER BY version DESC LIMIT 1`), &s); err != nil {
		return nil, err
	}
	return policy.Restore(s)
}

// Save stores a new version; an existing version is ports.ErrConcurrentUpdate.
func (r *PolicyRepository) Save(ctx context.Context, p *policy.Policy) error {
	s := p.Snapshot()
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal policy: %w", err)
	}
	if _, err := r.q(ctx).Exec(ctx, `INSERT INTO promise_policies (version, snapshot) VALUES ($1, $2)`, s.Version, raw); err != nil {
		if isUnique(err) {
			return ports.ErrConcurrentUpdate
		}
		return err
	}
	return nil
}
