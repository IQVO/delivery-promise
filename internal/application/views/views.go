// Package views holds the read DTOs of the application: what a query or a
// command returns to an adapter. They carry the wire names of apis/openapi.yaml
// (camelCase, optional fields omitted when absent, arrays always present,
// instants in UTC).
package views

import (
	"slices"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// Line is a shipment group line.
type Line struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

// Window is a delivery window.
type Window struct {
	Earliest time.Time `json:"earliest"`
	Latest   time.Time `json:"latest"`
}

// Witness is the witness path.
type Witness struct {
	NodeID            string   `json:"nodeId"`
	PickupDepartureID string   `json:"pickupDepartureId,omitempty"`
	Legs              []string `json:"legs"`
	DeliveryWaveID    string   `json:"deliveryWaveId,omitempty"`
}

// Offer is a delivery offer.
type Offer struct {
	OfferID                 string    `json:"offerId"`
	State                   string    `json:"state"`
	GroupNo                 int       `json:"groupNo"`
	DestinationPostalPrefix string    `json:"destinationPostalPrefix"`
	Tier                    string    `json:"tier"`
	Lines                   []Line    `json:"lines"`
	Window                  Window    `json:"window"`
	WitnessPath             Witness   `json:"witnessPath"`
	Basis                   string    `json:"basis"`
	PolicyVersion           string    `json:"policyVersion"`
	QuotedAt                time.Time `json:"quotedAt"`
	ExpiresAt               time.Time `json:"expiresAt"`
}

// Unavailable says why a (group, tier) has no offer.
type Unavailable struct {
	Tier       string `json:"tier"`
	Constraint string `json:"constraint"`
	GroupNo    int    `json:"groupNo,omitempty"`
}

// QuoteResult is the answer of QuoteDeliveryOffers.
type QuoteResult struct {
	Offers      []Offer       `json:"offers"`
	Unavailable []Unavailable `json:"unavailable"`
}

// Terms are the promise terms.
type Terms struct {
	Tier              string    `json:"tier"`
	ShipBy            time.Time `json:"shipBy"`
	DeliverByEarliest time.Time `json:"deliverByEarliest"`
	DeliverByLatest   time.Time `json:"deliverByLatest"`
	Basis             string    `json:"basis"`
	PolicyVersion     string    `json:"policyVersion"`
}

// Revision is one re-promise.
type Revision struct {
	RevisionNo    int       `json:"revisionNo"`
	PreviousTerms Terms     `json:"previousTerms"`
	Reason        string    `json:"reason"`
	RevisedAt     time.Time `json:"revisedAt"`
}

// Judgement is the verdict on a promise.
type Judgement struct {
	Outcome       string     `json:"outcome"`
	DeliveredAt   *time.Time `json:"deliveredAt,omitempty"`
	LateByMinutes *int       `json:"lateByMinutes,omitempty"`
	Cause         string     `json:"cause,omitempty"`
}

// Promise is a promise.
type Promise struct {
	PromiseID               string     `json:"promiseId"`
	OfferID                 string     `json:"offerId"`
	OrderID                 string     `json:"orderId,omitempty"`
	GroupNo                 int        `json:"groupNo,omitempty"`
	State                   string     `json:"state"`
	DestinationPostalPrefix string     `json:"destinationPostalPrefix"`
	Lines                   []Line     `json:"lines"`
	Terms                   Terms      `json:"terms"`
	WitnessPath             Witness    `json:"witnessPath"`
	HoldExpiresAt           time.Time  `json:"holdExpiresAt"`
	Revisions               []Revision `json:"revisions"`
	Judgement               *Judgement `json:"judgement,omitempty"`
	AtRiskReason            string     `json:"atRiskReason,omitempty"`
	CancelReason            string     `json:"cancelReason,omitempty"`
	ReleaseReason           string     `json:"releaseReason,omitempty"`
	Version                 int64      `json:"version"`
}

// PromiseList is the answer of GET /promises?orderId=.
type PromiseList struct {
	Items []Promise `json:"items"`
}

// Capacity is the capacity of a promise book.
type Capacity struct {
	Share       int `json:"share"`
	Provisional int `json:"provisional"`
	Confirmed   int `json:"confirmed"`
}

// Book is a promise book.
type Book struct {
	BookID            string    `json:"bookId"`
	NodeID            string    `json:"nodeId"`
	PickupDepartureID string    `json:"pickupDepartureId"`
	CutoffAt          time.Time `json:"cutoffAt"`
	State             string    `json:"state"`
	Capacity          Capacity  `json:"capacity"`
	Version           int64     `json:"version"`
}

// BookList is the answer of GET /promise-books.
type BookList struct {
	Items []Book `json:"items"`
}

// OrderDeadline is a per-tier, per-region order deadline.
type OrderDeadline struct {
	Tier      string `json:"tier"`
	RegionID  string `json:"regionId,omitempty"`
	LocalTime string `json:"localTime"`
}

// Policy is a promise policy version.
type Policy struct {
	PolicyVersion        string          `json:"policyVersion"`
	Version              int             `json:"version"`
	OfferTTLMinutes      int             `json:"offerTtlMinutes"`
	HoldTTLMinutes       int             `json:"holdTtlMinutes"`
	PackagesPerOrder     int             `json:"packagesPerOrder"`
	CapacitySharePercent int             `json:"capacitySharePercent"`
	OrderDeadlines       []OrderDeadline `json:"orderDeadlines"`
	PublishedAt          time.Time       `json:"publishedAt"`
}

func lines(in []shared.Line) []Line {
	out := make([]Line, len(in))
	for i, l := range in {
		out[i] = Line{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity}
	}
	return out
}

func witness(w shared.Witness) Witness {
	legs := slices.Clone(w.Legs)
	if legs == nil {
		legs = []string{}
	}
	return Witness{NodeID: w.NodeID, PickupDepartureID: w.PickupDepartureID, Legs: legs, DeliveryWaveID: w.DeliveryWaveID}
}

func terms(t promise.Terms) Terms {
	return Terms{
		Tier: string(t.Tier), ShipBy: t.ShipBy.UTC(), DeliverByEarliest: t.Window.Earliest.UTC(), DeliverByLatest: t.Window.Latest.UTC(),
		Basis: string(t.Basis), PolicyVersion: t.PolicyVersion,
	}
}

// FromOffer maps an offer aggregate.
func FromOffer(o *offer.Offer) Offer {
	return Offer{
		OfferID: string(o.ID()), State: string(o.State()), GroupNo: o.GroupNo(), DestinationPostalPrefix: o.DestinationPrefix(),
		Tier: string(o.Tier()), Lines: lines(o.Lines()), Window: Window{Earliest: o.Window().Earliest.UTC(), Latest: o.Window().Latest.UTC()},
		WitnessPath: witness(o.Witness()), Basis: string(o.Basis()), PolicyVersion: o.PolicyVersion(), QuotedAt: o.QuotedAt().UTC(), ExpiresAt: o.ExpiresAt().UTC(),
	}
}

// FromPromise maps a promise aggregate.
func FromPromise(p *promise.Promise) Promise {
	revs := make([]Revision, 0, len(p.Revisions()))
	for _, r := range p.Revisions() {
		revs = append(revs, Revision{RevisionNo: r.No, PreviousTerms: terms(r.PreviousTerms), Reason: r.Reason, RevisedAt: r.RevisedAt.UTC()})
	}
	v := Promise{
		PromiseID: string(p.ID()), OfferID: p.OfferID(), OrderID: p.OrderID(), GroupNo: p.GroupNo(), State: string(p.State()),
		DestinationPostalPrefix: p.DestinationPrefix(), Lines: lines(p.Lines()), Terms: terms(p.Terms()), WitnessPath: witness(p.Witness()),
		HoldExpiresAt: p.HoldExpiresAt().UTC(), Revisions: revs, AtRiskReason: p.AtRiskReason(), CancelReason: p.CancelReason(),
		ReleaseReason: p.ReleaseReason(), Version: p.Version(),
	}
	if j := p.Judgement(); j != nil {
		v.Judgement = judgement(*j)
	}
	return v
}

func judgement(j promise.Judgement) *Judgement {
	out := &Judgement{Outcome: string(j.Outcome), Cause: string(j.Cause)}
	if !j.DeliveredAt.IsZero() {
		d := j.DeliveredAt.UTC()
		out.DeliveredAt = &d
	}
	if j.Outcome == promise.OutcomeBroken {
		m := j.LateByMinutes
		out.LateByMinutes = &m
	}
	return out
}

// FromBook maps a book aggregate.
func FromBook(b *book.Book) Book {
	return Book{
		BookID: string(b.ID()), NodeID: b.NodeID(), PickupDepartureID: b.PickupDepartureID(), CutoffAt: b.CutoffAt().UTC(), State: string(b.State()),
		Capacity: Capacity{Share: b.Share(), Provisional: b.Provisional(), Confirmed: b.Confirmed()}, Version: b.Version(),
	}
}

// FromPolicy maps a policy.
func FromPolicy(p *policy.Policy) Policy {
	ds := make([]OrderDeadline, 0, len(p.Deadlines()))
	for _, d := range p.Deadlines() {
		ds = append(ds, OrderDeadline{Tier: string(d.Tier), RegionID: d.RegionID, LocalTime: d.ClockOf()})
	}
	return Policy{
		PolicyVersion: p.VersionName(), Version: p.Version(), OfferTTLMinutes: int(p.OfferTTL() / time.Minute), HoldTTLMinutes: int(p.HoldTTL() / time.Minute),
		PackagesPerOrder: p.PackagesPerOrder(), CapacitySharePercent: p.SharePercent(), OrderDeadlines: ds, PublishedAt: p.PublishedAt().UTC(),
	}
}
