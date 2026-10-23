// Package policy is the PromisePolicy aggregate (gap G19): a versioned set of
// promise parameters (offer and hold lifetimes, packages per order, the share
// of node capacity the promise side may use) and the per-tier, per-region
// order deadlines (TierOrderDeadline). A published version is immutable; the
// current policy is the highest version, and version 0 is the built-in default.
package policy

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/calendar"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// Bounds of the policy parameters (plain literals, ADR 0002 A29).
const (
	MaxTTLMinutes         = 1440
	MaxPackagesPerOrder   = 100
	MaxSharePercent       = 100
	MaxDeadlines          = 200
	DefaultOfferTTL       = 10
	DefaultHoldTTL        = 15
	DefaultPackagesPerOrd = 1
	DefaultSharePercent   = 100
)

// ErrVersionNotNext marks a publish whose version is not the stored one + 1.
var ErrVersionNotNext = errors.New("policy version is not the next one")

// Deadline is the local order deadline of one tier in one region. RegionID ""
// means every region.
type Deadline struct {
	Tier     shared.Tier
	RegionID string
	Minute   int // local minutes since midnight, in the destination station's time zone
}

// Policy is the aggregate root.
type Policy struct {
	version          int
	offerTTL         int
	holdTTL          int
	packagesPerOrder int
	sharePercent     int
	deadlines        []Deadline
	publishedAt      time.Time
}

// Snapshot is the persisted form of a policy. Restore validates it.
type Snapshot struct {
	Version          int
	OfferTTLMinutes  int
	HoldTTLMinutes   int
	PackagesPerOrder int
	SharePercent     int
	Deadlines        []Deadline
	PublishedAt      time.Time
}

// Input is the data of PublishPromisePolicy.
type Input struct {
	OfferTTLMinutes  int
	HoldTTLMinutes   int
	PackagesPerOrder int
	SharePercent     int
	Deadlines        []Deadline
}

// Default returns version 0: the policy in force before anything is published.
func Default() *Policy {
	return &Policy{
		version: 0, offerTTL: DefaultOfferTTL, holdTTL: DefaultHoldTTL,
		packagesPerOrder: DefaultPackagesPerOrd, sharePercent: DefaultSharePercent,
	}
}

func validate(in Input) error {
	switch {
	case in.OfferTTLMinutes < 1 || in.OfferTTLMinutes > MaxTTLMinutes:
		return shared.Invalid("offerTtlMinutes must be between 1 and %d", MaxTTLMinutes)
	case in.HoldTTLMinutes < 1 || in.HoldTTLMinutes > MaxTTLMinutes:
		return shared.Invalid("holdTtlMinutes must be between 1 and %d", MaxTTLMinutes)
	case in.PackagesPerOrder < 1 || in.PackagesPerOrder > MaxPackagesPerOrder:
		return shared.Invalid("packagesPerOrder must be between 1 and %d", MaxPackagesPerOrder)
	case in.SharePercent < 1 || in.SharePercent > MaxSharePercent:
		return shared.Invalid("capacitySharePercent must be between 1 and %d", MaxSharePercent)
	}
	return validateDeadlines(in.Deadlines)
}

func validateDeadlines(ds []Deadline) error {
	if len(ds) > MaxDeadlines {
		return shared.Invalid("at most %d order deadlines", MaxDeadlines)
	}
	type key struct {
		t shared.Tier
		r string
	}
	seen := map[key]bool{}
	for _, d := range ds {
		if err := validateDeadline(d); err != nil {
			return err
		}
		k := key{d.Tier, d.RegionID}
		if seen[k] {
			return shared.Invalid("two deadlines for tier %s and region %q", d.Tier, d.RegionID)
		}
		seen[k] = true
	}
	return nil
}

func validateDeadline(d Deadline) error {
	switch {
	case !d.Tier.Valid():
		return shared.Invalid("deadline tier %q is not a tier", d.Tier)
	case d.RegionID != "" && !shared.ValidID(d.RegionID):
		return shared.Invalid("deadline regionId %q is not a valid id", d.RegionID)
	case d.Minute < 0 || d.Minute >= 24*60:
		return shared.Invalid("deadline localTime must be between 00:00 and 23:59")
	}
	return nil
}

func sortedDeadlines(in []Deadline) []Deadline {
	out := slices.Clone(in)
	slices.SortFunc(out, func(a, b Deadline) int {
		if c := a.Tier.Rank() - b.Tier.Rank(); c != 0 {
			return c
		}
		if a.RegionID < b.RegionID {
			return -1
		}
		if a.RegionID > b.RegionID {
			return 1
		}
		return 0
	})
	return out
}

// Publish creates policy version current+1 (current is the highest stored
// version, 0 when none) and raises PromisePolicyPublished.
func Publish(current int, in Input, now time.Time) (*Policy, []shared.Event, error) {
	if current < 0 || current >= shared.MaxInt32 {
		return nil, nil, fmt.Errorf("%w: current version %d", ErrVersionNotNext, current)
	}
	if err := validate(in); err != nil {
		return nil, nil, err
	}
	p := &Policy{
		version: current + 1, offerTTL: in.OfferTTLMinutes, holdTTL: in.HoldTTLMinutes,
		packagesPerOrder: in.PackagesPerOrder, sharePercent: in.SharePercent,
		deadlines: sortedDeadlines(in.Deadlines), publishedAt: shared.UTC(now),
	}
	ev := PromisePolicyPublished{
		Header: shared.Header{Version: int64(p.version), At: p.publishedAt}, PolicyVersion: p.VersionName(), Number: p.version,
		OfferTTLMinutes: p.offerTTL, HoldTTLMinutes: p.holdTTL, PackagesPerOrder: p.packagesPerOrder,
		SharePercent: p.sharePercent, Deadlines: slices.Clone(p.deadlines), PublishedAt: p.publishedAt,
	}
	return p, []shared.Event{ev}, nil
}

// Restore rebuilds a persisted policy, validating every invariant. Version 0
// is only valid as the default.
func Restore(s Snapshot) (*Policy, error) {
	in := Input{
		OfferTTLMinutes: s.OfferTTLMinutes, HoldTTLMinutes: s.HoldTTLMinutes, PackagesPerOrder: s.PackagesPerOrder,
		SharePercent: s.SharePercent, Deadlines: s.Deadlines,
	}
	if err := validate(in); err != nil {
		return nil, shared.Corrupt("policy %d: %v", s.Version, err)
	}
	if s.Version < 1 || s.Version > shared.MaxInt32 || s.PublishedAt.IsZero() {
		return nil, shared.Corrupt("policy %d: a version of at least 1 and publishedAt are required", s.Version)
	}
	return &Policy{
		version: s.Version, offerTTL: s.OfferTTLMinutes, holdTTL: s.HoldTTLMinutes, packagesPerOrder: s.PackagesPerOrder,
		sharePercent: s.SharePercent, deadlines: sortedDeadlines(s.Deadlines), publishedAt: shared.UTC(s.PublishedAt),
	}, nil
}

// Version returns the version number (0 for the default).
func (p *Policy) Version() int { return p.version }

// VersionName returns "v" + the version number: the policyVersion recorded on
// offers and promises.
func (p *Policy) VersionName() string { return "v" + strconv.Itoa(p.version) }

// OfferTTL returns how long a quote stays QUOTED.
func (p *Policy) OfferTTL() time.Duration { return time.Duration(p.offerTTL) * time.Minute }

// HoldTTL returns how long a PROVISIONAL promise holds capacity.
func (p *Policy) HoldTTL() time.Duration { return time.Duration(p.holdTTL) * time.Minute }

// PackagesPerOrder returns the declared packages per order.
func (p *Policy) PackagesPerOrder() int { return p.packagesPerOrder }

// SharePercent returns the share of cutoff capacity the promise side may use.
func (p *Policy) SharePercent() int { return p.sharePercent }

// Deadlines returns a copy of the order deadlines.
func (p *Policy) Deadlines() []Deadline { return slices.Clone(p.deadlines) }

// PublishedAt returns when the version was published (zero for the default).
func (p *Policy) PublishedAt() time.Time { return p.publishedAt }

// Snapshot returns the persisted form.
func (p *Policy) Snapshot() Snapshot {
	return Snapshot{
		Version: p.version, OfferTTLMinutes: p.offerTTL, HoldTTLMinutes: p.holdTTL, PackagesPerOrder: p.packagesPerOrder,
		SharePercent: p.sharePercent, Deadlines: slices.Clone(p.deadlines), PublishedAt: p.publishedAt,
	}
}

// PastDeadline reports whether the order deadline of tier in regionID has
// passed at the local minute-of-day localMinute. A region-specific deadline
// overrides an all-region one; no deadline means the tier is never past it.
func (p *Policy) PastDeadline(tier shared.Tier, regionID string, localMinute int) bool {
	var all, specific *Deadline
	for i := range p.deadlines {
		d := &p.deadlines[i]
		if d.Tier != tier {
			continue
		}
		switch d.RegionID {
		case "":
			all = d
		case regionID:
			specific = d
		}
	}
	d := specific
	if d == nil {
		d = all
	}
	return d != nil && localMinute > d.Minute
}

// ClockOf formats a deadline's local time "HH:mm".
func (d Deadline) ClockOf() string { return calendar.Clock(d.Minute) }
