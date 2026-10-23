package policy

import (
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// PromisePolicyPublished is raised by Publish (analytics only).
type PromisePolicyPublished struct {
	shared.Header
	PolicyVersion    string
	Number           int
	OfferTTLMinutes  int
	HoldTTLMinutes   int
	PackagesPerOrder int
	SharePercent     int
	Deadlines        []Deadline
	PublishedAt      time.Time
}

// EventName returns "PromisePolicyPublished".
func (PromisePolicyPublished) EventName() string { return "PromisePolicyPublished" }

// Entity returns "promisepolicy".
func (PromisePolicyPublished) Entity() string { return "promisepolicy" }

// SubjectID returns the policyVersion.
func (e PromisePolicyPublished) SubjectID() string { return e.PolicyVersion }

var _ shared.Event = PromisePolicyPublished{}
