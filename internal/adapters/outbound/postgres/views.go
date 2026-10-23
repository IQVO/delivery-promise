package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/search"
)

// getJSON reads the data column of a read-model row; a miss is ports.ErrNotFound.
func getJSON[V any](ctx context.Context, q querier, sql string, key string) (V, error) {
	var v V
	return v, scanSnapshot(q.QueryRow(ctx, sql, key), &v)
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil { // the read-model structs hold only strings, numbers, times and maps of them
		panic("postgres: marshal read model: " + err.Error())
	}
	return raw
}

func collect[V any](rows pgx.Rows, err error) ([]V, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []V
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v V
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// NetworkViews is the Postgres ports.NetworkViews.
type NetworkViews struct{ db }

// NewNetworkViews constructs NetworkViews over pool.
func NewNetworkViews(pool *pgxpool.Pool) *NetworkViews { return &NetworkViews{db{pool}} }

func (v *NetworkViews) put(ctx context.Context, sql string, args ...any) error {
	_, err := v.q(ctx).Exec(ctx, sql, args...)
	return err
}

// GetNode returns the node row or ports.ErrNotFound.
func (v *NetworkViews) GetNode(ctx context.Context, id string) (readmodel.Node, error) {
	return getJSON[readmodel.Node](ctx, v.q(ctx), `SELECT data FROM rm_nodes WHERE id = $1`, id)
}

// PutNode stores the node row.
func (v *NetworkViews) PutNode(ctx context.Context, n readmodel.Node) error {
	return v.put(ctx, `INSERT INTO rm_nodes (id, data) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data`, n.ID, mustJSON(n))
}

// GetRegion returns the region row or ports.ErrNotFound.
func (v *NetworkViews) GetRegion(ctx context.Context, id string) (readmodel.Region, error) {
	return getJSON[readmodel.Region](ctx, v.q(ctx), `SELECT data FROM rm_regions WHERE id = $1`, id)
}

// PutRegion stores the region row.
func (v *NetworkViews) PutRegion(ctx context.Context, r readmodel.Region) error {
	return v.put(ctx, `INSERT INTO rm_regions (id, data) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data`, r.ID, mustJSON(r))
}

// GetArea returns the service-area row or ports.ErrNotFound.
func (v *NetworkViews) GetArea(ctx context.Context, prefix string) (readmodel.Area, error) {
	return getJSON[readmodel.Area](ctx, v.q(ctx), `SELECT data FROM rm_areas WHERE postal_prefix = $1`, prefix)
}

// PutArea stores the service-area row.
func (v *NetworkViews) PutArea(ctx context.Context, a readmodel.Area) error {
	return v.put(ctx, `INSERT INTO rm_areas (postal_prefix, data) VALUES ($1, $2) ON CONFLICT (postal_prefix) DO UPDATE SET data = EXCLUDED.data`, a.PostalPrefix, mustJSON(a))
}

// GetLane returns the lane row or ports.ErrNotFound.
func (v *NetworkViews) GetLane(ctx context.Context, id string) (search.Lane, error) {
	return getJSON[search.Lane](ctx, v.q(ctx), `SELECT data FROM rm_lanes WHERE id = $1`, id)
}

// PutLane stores the lane row.
func (v *NetworkViews) PutLane(ctx context.Context, l search.Lane) error {
	return v.put(ctx, `INSERT INTO rm_lanes (id, data) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data`, l.ID, mustJSON(l))
}

// GetItem returns the item row or ports.ErrNotFound.
func (v *NetworkViews) GetItem(ctx context.Context, id string) (readmodel.Item, error) {
	return getJSON[readmodel.Item](ctx, v.q(ctx), `SELECT data FROM rm_items WHERE id = $1`, id)
}

// PutItem stores the item row.
func (v *NetworkViews) PutItem(ctx context.Context, i readmodel.Item) error {
	return v.put(ctx, `INSERT INTO rm_items (id, data) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data`, i.ID, mustJSON(i))
}

// GetListing returns the listing row or ports.ErrNotFound.
func (v *NetworkViews) GetListing(ctx context.Context, id string) (readmodel.Listing, error) {
	return getJSON[readmodel.Listing](ctx, v.q(ctx), `SELECT data FROM rm_listings WHERE id = $1`, id)
}

// PutListing stores the listing row.
func (v *NetworkViews) PutListing(ctx context.Context, l readmodel.Listing) error {
	return v.put(ctx, `INSERT INTO rm_listings (id, item_id, node_id, data) VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET item_id = EXCLUDED.item_id, node_id = EXCLUDED.node_id, data = EXCLUDED.data`, l.ID, l.ItemID, l.NodeID, mustJSON(l))
}

// ListingsForNode returns the node's listings of the items by listing id.
func (v *NetworkViews) ListingsForNode(ctx context.Context, nodeID string, itemIDs []string) ([]readmodel.Listing, error) {
	return collect[readmodel.Listing](v.q(ctx).Query(ctx, `SELECT data FROM rm_listings WHERE node_id = $1 AND item_id = ANY ($2) ORDER BY id`, nodeID, itemIDs))
}

// GetDeparture returns the departure row or ports.ErrNotFound.
func (v *NetworkViews) GetDeparture(ctx context.Context, id string) (search.Departure, error) {
	return getJSON[search.Departure](ctx, v.q(ctx), `SELECT data FROM rm_departures WHERE id = $1`, id)
}

// PutDeparture stores the departure row.
func (v *NetworkViews) PutDeparture(ctx context.Context, d search.Departure) error {
	return v.put(ctx, `INSERT INTO rm_departures (id, cutoff_at, scheduled, data) VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET cutoff_at = EXCLUDED.cutoff_at, scheduled = EXCLUDED.scheduled, data = EXCLUDED.data`,
		d.ID, d.CutoffAt, d.Scheduled, mustJSON(d))
}

// GetWave returns the wave row or ports.ErrNotFound.
func (v *NetworkViews) GetWave(ctx context.Context, id string) (search.Wave, error) {
	return getJSON[search.Wave](ctx, v.q(ctx), `SELECT data FROM rm_waves WHERE id = $1`, id)
}

// PutWave stores the wave row.
func (v *NetworkViews) PutWave(ctx context.Context, w search.Wave) error {
	return v.put(ctx, `INSERT INTO rm_waves (id, station_id, window_end, scheduled, data) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET station_id = EXCLUDED.station_id, window_end = EXCLUDED.window_end, scheduled = EXCLUDED.scheduled, data = EXCLUDED.data`,
		w.ID, w.Station, w.WindowEnd, w.Scheduled, mustJSON(w))
}

// GetScorecard returns the scorecard row or ports.ErrNotFound.
func (v *NetworkViews) GetScorecard(ctx context.Context, nodeID string) (readmodel.Scorecard, error) {
	return getJSON[readmodel.Scorecard](ctx, v.q(ctx), `SELECT data FROM rm_scorecards WHERE node_id = $1`, nodeID)
}

// PutScorecard stores the scorecard row.
func (v *NetworkViews) PutScorecard(ctx context.Context, sc readmodel.Scorecard) error {
	return v.put(ctx, `INSERT INTO rm_scorecards (node_id, data) VALUES ($1, $2) ON CONFLICT (node_id) DO UPDATE SET data = EXCLUDED.data`, sc.NodeID, mustJSON(sc))
}

// Load returns the slice of the network a search needs: the listings of the
// items, the scheduled departures whose cutoff lies in [From, Until), the
// scheduled waves of the station that have not ended before From, every lane,
// and the node, scorecard and item rows they refer to.
func (v *NetworkViews) Load(ctx context.Context, q readmodel.Query) (readmodel.Network, error) {
	var net readmodel.Network
	var err error
	qq := v.q(ctx)
	if net.Listings, err = collect[readmodel.Listing](qq.Query(ctx, `SELECT data FROM rm_listings WHERE item_id = ANY ($1) ORDER BY id`, q.ItemIDs)); err != nil {
		return net, err
	}
	if net.Departures, err = collect[search.Departure](qq.Query(ctx, `SELECT data FROM rm_departures WHERE scheduled AND cutoff_at >= $1 AND cutoff_at < $2 ORDER BY id`, q.From, q.Until)); err != nil {
		return net, err
	}
	if net.Waves, err = collect[search.Wave](qq.Query(ctx, `SELECT data FROM rm_waves WHERE scheduled AND station_id = $1 AND window_end >= $2 ORDER BY id`, q.StationID, q.From)); err != nil {
		return net, err
	}
	if net.Lanes, err = collect[search.Lane](qq.Query(ctx, `SELECT data FROM rm_lanes ORDER BY id`)); err != nil {
		return net, err
	}
	nodeIDs := []string{q.StationID}
	for _, l := range net.Listings {
		nodeIDs = append(nodeIDs, l.NodeID)
	}
	for _, d := range net.Departures {
		nodeIDs = append(nodeIDs, d.Origin, d.Dest)
	}
	slices.Sort(nodeIDs)
	nodeIDs = slices.Compact(nodeIDs)
	if net.Nodes, err = collect[readmodel.Node](qq.Query(ctx, `SELECT data FROM rm_nodes WHERE id = ANY ($1) ORDER BY id`, nodeIDs)); err != nil {
		return net, err
	}
	if net.Scorecards, err = collect[readmodel.Scorecard](qq.Query(ctx, `SELECT data FROM rm_scorecards WHERE node_id = ANY ($1) ORDER BY node_id`, nodeIDs)); err != nil {
		return net, err
	}
	net.Items, err = collect[readmodel.Item](qq.Query(ctx, `SELECT data FROM rm_items WHERE id = ANY ($1) ORDER BY id`, q.ItemIDs))
	return net, err
}

// ShipmentViews is the Postgres ports.ShipmentViews.
type ShipmentViews struct{ db }

// NewShipmentViews constructs ShipmentViews over pool.
func NewShipmentViews(pool *pgxpool.Pool) *ShipmentViews { return &ShipmentViews{db{pool}} }

// GetAssignment returns the assignment or ports.ErrNotFound.
func (v *ShipmentViews) GetAssignment(ctx context.Context, id string) (readmodel.Assignment, error) {
	return getJSON[readmodel.Assignment](ctx, v.q(ctx), `SELECT data FROM rm_assignments WHERE id = $1`, id)
}

// PutAssignment stores the assignment.
func (v *ShipmentViews) PutAssignment(ctx context.Context, a readmodel.Assignment) error {
	_, err := v.q(ctx).Exec(ctx, `INSERT INTO rm_assignments (id, promise_id, data) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET promise_id = EXCLUDED.promise_id, data = EXCLUDED.data`, a.ID, a.PromiseID, mustJSON(a))
	return err
}

// FindAssignmentByPromise returns the assignment of the promise.
func (v *ShipmentViews) FindAssignmentByPromise(ctx context.Context, promiseID string) (readmodel.Assignment, error) {
	return getJSON[readmodel.Assignment](ctx, v.q(ctx), `SELECT data FROM rm_assignments WHERE promise_id = $1 ORDER BY id LIMIT 1`, promiseID)
}

// PutShipment records the tracking numbers of an assignment.
func (v *ShipmentViews) PutShipment(ctx context.Context, assignmentID string, trackingNumbers []string) error {
	if trackingNumbers == nil {
		trackingNumbers = []string{}
	}
	q := v.q(ctx)
	if _, err := q.Exec(ctx, `INSERT INTO rm_shipments (assignment_id, tracking_numbers) VALUES ($1, $2)
		ON CONFLICT (assignment_id) DO UPDATE SET tracking_numbers = EXCLUDED.tracking_numbers`, assignmentID, trackingNumbers); err != nil {
		return err
	}
	for _, t := range trackingNumbers {
		if _, err := q.Exec(ctx, `INSERT INTO rm_trackings (tracking_number, assignment_id) VALUES ($1, $2)
			ON CONFLICT (tracking_number) DO UPDATE SET assignment_id = EXCLUDED.assignment_id`, t, assignmentID); err != nil {
			return err
		}
	}
	return nil
}

// Shipment returns the assignment's tracking numbers, sorted.
func (v *ShipmentViews) Shipment(ctx context.Context, assignmentID string) ([]string, error) {
	var out []string
	err := v.q(ctx).QueryRow(ctx, `SELECT tracking_numbers FROM rm_shipments WHERE assignment_id = $1`, assignmentID).Scan(&out)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	slices.Sort(out)
	return out, err
}

// AssignmentOf returns the assignment a tracking number was shipped under.
func (v *ShipmentViews) AssignmentOf(ctx context.Context, trackingNumber string) (string, error) {
	var id string
	err := v.q(ctx).QueryRow(ctx, `SELECT assignment_id FROM rm_trackings WHERE tracking_number = $1`, trackingNumber).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ports.ErrNotFound
	}
	return id, err
}

// PutOutcome records the first final fact of a package; a later one is ignored.
func (v *ShipmentViews) PutOutcome(ctx context.Context, o readmodel.PackageOutcome) error {
	_, err := v.q(ctx).Exec(ctx, `INSERT INTO rm_outcomes (tracking_number, data) VALUES ($1, $2) ON CONFLICT (tracking_number) DO NOTHING`, o.TrackingNumber, mustJSON(o))
	return err
}

// Outcomes returns the recorded outcomes of the tracking numbers, by tracking number.
func (v *ShipmentViews) Outcomes(ctx context.Context, trackingNumbers []string) ([]readmodel.PackageOutcome, error) {
	return collect[readmodel.PackageOutcome](v.q(ctx).Query(ctx, `SELECT data FROM rm_outcomes WHERE tracking_number = ANY ($1) ORDER BY tracking_number`, trackingNumbers))
}

// Inbox is the Postgres ports.Inbox (consumer idempotency).
type Inbox struct{ db }

// NewInbox constructs an Inbox over pool.
func NewInbox(pool *pgxpool.Pool) *Inbox { return &Inbox{db{pool}} }

// Begin records the CloudEvents id and reports whether it is new. Inside a unit
// of work the row commits with the event's side effects, so a rollback un-claims
// it and a redelivery is processed, not skipped. A concurrent claim of the same
// id blocks on the primary key until the other transaction ends.
func (i *Inbox) Begin(ctx context.Context, eventID, eventType string, at time.Time) (bool, error) {
	tag, err := i.q(ctx).Exec(ctx, `INSERT INTO processed_events (event_id, event_type, processed_at) VALUES ($1, $2, $3) ON CONFLICT (event_id) DO NOTHING`, eventID, eventType, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
