// Package offer is the DeliveryOffer aggregate: a short-lived quote for one
// shipment group at one tier, with its delivery window, the witness path that
// proves it, its basis and the policy version it was quoted under.
//
// States: QUOTED, EXPIRED, CONSUMED; transitions QUOTED -> CONSUMED (Consume,
// from MakePromise) and QUOTED -> EXPIRED (Expire, ExpireOffers). Invariants:
// expiresAt <= min(quotedAt + offerTtl, the latest instant the witness pickup
// cutoff can still be met) (ErrOfferExpiryTooLate); an expired or consumed
// offer cannot back a promise (ErrOfferNotQuotable); a fast tier is never
// NOMINAL (ErrNominalBasisNotAllowedForTier). The offer raises the analytics
// event DeliveryOfferQuoted.
package offer

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// The offer invariants of the model, by the model's names.
var (
	// ErrOfferExpiryTooLate: expiresAt is later than the TTL or the latest
	// meetable cutoff allow. Never returned over HTTP: the search does not
	// build such an offer.
	ErrOfferExpiryTooLate = errors.New("offer expiry too late")
	// ErrOfferNotQuotable: the offer is expired or consumed (409).
	ErrOfferNotQuotable = errors.New("offer not quotable")
	// ErrNominalBasisNotAllowedForTier: only STANDARD may be NOMINAL (422).
	ErrNominalBasisNotAllowedForTier = errors.New("nominal basis not allowed for tier")
)

// ID identifies an offer (offerId).
type ID string

// State is the lifecycle state of an offer.
type State string

// The three offer states.
const (
	StateQuoted   State = "QUOTED"
	StateExpired  State = "EXPIRED"
	StateConsumed State = "CONSUMED"
)

// Valid reports whether s is one of the three states.
func (s State) Valid() bool { return s == StateQuoted || s == StateExpired || s == StateConsumed }

// Offer is the aggregate root.
type Offer struct {
	id           ID
	groupNo      int
	destination  string
	tier         shared.Tier
	lines        []shared.Line
	window       shared.Window
	witness      shared.Witness
	basis        shared.Basis
	policy       string
	quotedAt     time.Time
	expiresAt    time.Time
	state        State
	closedAt     time.Time
	version      int64
	pickupCutoff time.Time
}

// Snapshot is the persisted form of an offer. Restore validates it.
type Snapshot struct {
	ID                ID
	GroupNo           int
	DestinationPrefix string
	Tier              shared.Tier
	Lines             []shared.Line
	Window            shared.Window
	Witness           shared.Witness
	Basis             shared.Basis
	PolicyVersion     string
	QuotedAt          time.Time
	ExpiresAt         time.Time
	State             State
	ClosedAt          time.Time
	PickupCutoffAt    time.Time
	Version           int64
}

// Quote is the input of QuoteDeliveryOffers for one (group, tier).
type Quote struct {
	ID                ID
	GroupNo           int
	DestinationPrefix string
	Tier              shared.Tier
	Lines             []shared.Line
	Window            shared.Window
	Witness           shared.Witness
	Basis             shared.Basis
	PolicyVersion     string
	TTL               time.Duration
	// ExpiresAt is the expiry the search computed: min(now + TTL, the latest
	// instant the pickup cutoff can still be met).
	ExpiresAt time.Time
	// LatestMeetableAt is the latest instant the witness pickup cutoff can
	// still be met (cutoff minus handling, in business time).
	LatestMeetableAt time.Time
	// PickupCutoffAt is the witness pickup cutoff (shipBy).
	PickupCutoffAt time.Time
}

// New creates a QUOTED offer at version 1 and raises DeliveryOfferQuoted.
func New(q Quote, now time.Time) (*Offer, []shared.Event, error) {
	if err := validateQuote(q); err != nil {
		return nil, nil, err
	}
	now = shared.UTC(now)
	if !q.ExpiresAt.After(now) || q.ExpiresAt.After(now.Add(q.TTL)) || q.ExpiresAt.After(q.LatestMeetableAt) {
		return nil, nil, fmt.Errorf("%w: expiresAt %s, quotedAt %s", ErrOfferExpiryTooLate, q.ExpiresAt.UTC().Format(time.RFC3339), now.Format(time.RFC3339))
	}
	o := &Offer{
		id: q.ID, groupNo: q.GroupNo, destination: q.DestinationPrefix, tier: q.Tier, lines: slices.Clone(q.Lines),
		window: q.Window, witness: q.Witness.Clone(), basis: q.Basis, policy: q.PolicyVersion, quotedAt: now,
		expiresAt: shared.UTC(q.ExpiresAt), state: StateQuoted, version: 1, pickupCutoff: shared.UTC(q.PickupCutoffAt),
	}
	ev := DeliveryOfferQuoted{
		Header: shared.Header{Version: o.version, At: now}, OfferID: o.id, Tier: o.tier, DestinationPrefix: o.destination,
		NodeID: o.witness.NodeID, Window: o.window, Basis: o.basis, PolicyVersion: o.policy, ExpiresAt: o.expiresAt,
	}
	return o, []shared.Event{ev}, nil
}

func validateQuote(q Quote) error {
	switch {
	case !shared.ValidID(string(q.ID)):
		return shared.Invalid("offerId %q is not a valid id", q.ID)
	case q.GroupNo < 1 || q.GroupNo > shared.MaxLines:
		return shared.Invalid("groupNo must be between 1 and %d", shared.MaxLines)
	case !shared.ValidPostalPrefix(q.DestinationPrefix):
		return shared.Invalid("destinationPostalPrefix %q is not valid", q.DestinationPrefix)
	case !q.Tier.Valid():
		return shared.Invalid("tier %q is not a tier", q.Tier)
	case !q.Basis.Valid():
		return shared.Invalid("basis %q is not SCHEDULED or NOMINAL", q.Basis)
	case q.PolicyVersion == "" || len(q.PolicyVersion) > shared.MaxID:
		return shared.Invalid("policyVersion is required")
	case q.TTL <= 0:
		return shared.Invalid("the offer TTL must be positive")
	}
	if err := shared.ValidateLines(q.Lines); err != nil {
		return err
	}
	if err := q.Window.Validate(); err != nil {
		return err
	}
	if err := q.Witness.Validate(q.Basis); err != nil {
		return err
	}
	if q.Basis == shared.BasisNominal && !q.Tier.AllowsNominal() {
		return fmt.Errorf("%w: %s", ErrNominalBasisNotAllowedForTier, q.Tier)
	}
	return nil
}

// Restore rebuilds a persisted offer, validating every invariant.
func Restore(s Snapshot) (*Offer, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, shared.Corrupt("offer %q: %v", s.ID, err)
	}
	return &Offer{
		id: s.ID, groupNo: s.GroupNo, destination: s.DestinationPrefix, tier: s.Tier, lines: slices.Clone(s.Lines),
		window: s.Window, witness: s.Witness.Clone(), basis: s.Basis, policy: s.PolicyVersion, quotedAt: shared.UTC(s.QuotedAt),
		expiresAt: shared.UTC(s.ExpiresAt), state: s.State, closedAt: shared.UTC(s.ClosedAt), version: s.Version,
		pickupCutoff: shared.UTC(s.PickupCutoffAt),
	}, nil
}

func checkSnapshot(s Snapshot) error {
	if s.Version < 1 {
		return errors.New("a version of at least 1 is required")
	}
	if !s.State.Valid() {
		return fmt.Errorf("state %q is not QUOTED, EXPIRED or CONSUMED", s.State)
	}
	if s.QuotedAt.IsZero() || !s.ExpiresAt.After(s.QuotedAt) {
		return errors.New("expiresAt must be after quotedAt")
	}
	if err := validateQuote(Quote{
		ID: s.ID, GroupNo: s.GroupNo, DestinationPrefix: s.DestinationPrefix, Tier: s.Tier, Lines: s.Lines, Window: s.Window,
		Witness: s.Witness, Basis: s.Basis, PolicyVersion: s.PolicyVersion, TTL: time.Nanosecond,
	}); err != nil {
		return err
	}
	if s.State == StateQuoted && !s.ClosedAt.IsZero() {
		return errors.New("a QUOTED offer has no closedAt")
	}
	if s.State != StateQuoted && s.ClosedAt.IsZero() {
		return errors.New("an EXPIRED or CONSUMED offer needs closedAt")
	}
	return nil
}

// Consume moves a QUOTED offer, not yet past expiresAt, to CONSUMED. Anything
// else is ErrOfferNotQuotable.
func (o *Offer) Consume(now time.Time) error {
	if o.state != StateQuoted || !now.Before(o.expiresAt) {
		return fmt.Errorf("%w: offer %s is %s, expires %s", ErrOfferNotQuotable, o.id, o.state, o.expiresAt.Format(time.RFC3339))
	}
	o.state = StateConsumed
	o.closedAt = shared.UTC(now)
	o.version++
	return nil
}

// Expire moves a QUOTED offer that is past expiresAt to EXPIRED. An offer that
// is not QUOTED, or not yet past expiresAt, is an illegal transition.
func (o *Offer) Expire(now time.Time) error {
	if o.state != StateQuoted {
		return fmt.Errorf("%w: offer %s is %s", shared.ErrIllegalTransition, o.id, o.state)
	}
	if now.Before(o.expiresAt) {
		return fmt.Errorf("%w: offer %s expires %s", shared.ErrIllegalTransition, o.id, o.expiresAt.Format(time.RFC3339))
	}
	o.state = StateExpired
	o.closedAt = shared.UTC(now)
	o.version++
	return nil
}

// Quotable reports whether the offer can back a promise at now.
func (o *Offer) Quotable(now time.Time) bool {
	return o.state == StateQuoted && now.Before(o.expiresAt)
}

// ID returns the offer id.
func (o *Offer) ID() ID { return o.id }

// GroupNo returns the quote-relative shipment group number.
func (o *Offer) GroupNo() int { return o.groupNo }

// DestinationPrefix returns the destination postal prefix.
func (o *Offer) DestinationPrefix() string { return o.destination }

// Tier returns the service tier.
func (o *Offer) Tier() shared.Tier { return o.tier }

// Lines returns a copy of the group's lines.
func (o *Offer) Lines() []shared.Line { return slices.Clone(o.lines) }

// Window returns the delivery window.
func (o *Offer) Window() shared.Window { return o.window }

// Witness returns a copy of the witness path.
func (o *Offer) Witness() shared.Witness { return o.witness.Clone() }

// Basis returns SCHEDULED or NOMINAL.
func (o *Offer) Basis() shared.Basis { return o.basis }

// PolicyVersion returns the policy version of the quote.
func (o *Offer) PolicyVersion() string { return o.policy }

// QuotedAt returns when the offer was quoted.
func (o *Offer) QuotedAt() time.Time { return o.quotedAt }

// ExpiresAt returns when the offer stops being quotable.
func (o *Offer) ExpiresAt() time.Time { return o.expiresAt }

// PickupCutoffAt returns the witness pickup cutoff (shipBy).
func (o *Offer) PickupCutoffAt() time.Time { return o.pickupCutoff }

// State returns the lifecycle state.
func (o *Offer) State() State { return o.state }

// ClosedAt returns when the offer was consumed or expired (zero while QUOTED).
func (o *Offer) ClosedAt() time.Time { return o.closedAt }

// Version returns the aggregate version.
func (o *Offer) Version() int64 { return o.version }

// Snapshot returns the persisted form.
func (o *Offer) Snapshot() Snapshot {
	return Snapshot{
		ID: o.id, GroupNo: o.groupNo, DestinationPrefix: o.destination, Tier: o.tier, Lines: slices.Clone(o.lines), Window: o.window,
		Witness: o.witness.Clone(), Basis: o.basis, PolicyVersion: o.policy, QuotedAt: o.quotedAt, ExpiresAt: o.expiresAt,
		State: o.state, ClosedAt: o.closedAt, PickupCutoffAt: o.pickupCutoff, Version: o.version,
	}
}
