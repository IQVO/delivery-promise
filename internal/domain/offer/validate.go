package offer

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// validateTerms checks the policy version, the TTL and the capacity band.
func validateTerms(q Quote) error {
	if q.PolicyVersion == "" || len(q.PolicyVersion) > shared.MaxID {
		return shared.Invalid("policyVersion is required")
	}
	if q.CapacityBand != "" && !q.CapacityBand.Valid() {
		return shared.Invalid("capacityBand %q is not AMPLE, TIGHT or SCARCE", q.CapacityBand)
	}
	if q.TTL <= 0 {
		return shared.Invalid("the offer TTL must be positive")
	}
	if q.ConsolidationEligible != nil && q.Tier != shared.TierConsolidated {
		return shared.Invalid("consolidationEligible belongs to the CONSOLIDATED tier, not %s", q.Tier)
	}
	return nil
}

func validateQuote(q Quote) error {
	if !shared.ValidID(string(q.ID)) {
		return shared.Invalid("offerId %q is not a valid id", q.ID)
	}
	if q.GroupNo < 1 || q.GroupNo > shared.MaxLines {
		return shared.Invalid("groupNo must be between 1 and %d", shared.MaxLines)
	}
	if !shared.ValidPostalPrefix(q.DestinationPrefix) {
		return shared.Invalid("destinationPostalPrefix %q is not valid", q.DestinationPrefix)
	}
	if !q.Tier.Valid() {
		return shared.Invalid("tier %q is not a tier", q.Tier)
	}
	if !q.Basis.Valid() {
		return shared.Invalid("basis %q is not SCHEDULED, NOMINAL or MEASURED", q.Basis)
	}
	if err := validateTerms(q); err != nil {
		return err
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
		Witness: s.Witness, Basis: s.Basis, PolicyVersion: s.PolicyVersion, TTL: time.Nanosecond, ConsolidationEligible: s.ConsolidationEligible,
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
