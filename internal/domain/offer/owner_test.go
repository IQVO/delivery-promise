package offer

import (
	"errors"
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// TestOfferLinesKeepTheirOwner is the four-places rule for the owner of a line
// (ADR 0017): New, Snapshot and Restore carry it, and a malformed one is
// refused on the way in and on the way back.
func TestOfferLinesKeepTheirOwner(t *testing.T) {
	q := quote()
	q.Lines = []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2, OwnerRef: "S-7"}, {CatalogItemID: "ci-2002", Quantity: 1}}
	o, _, err := New(q, now)
	if err != nil {
		t.Fatal(err)
	}
	if o.Lines()[0].OwnerRef != "S-7" || o.Lines()[1].OwnerRef != "" {
		t.Fatalf("New lost the owners: %+v", o.Lines())
	}
	s := o.Snapshot()
	if s.Lines[0].OwnerRef != "S-7" {
		t.Fatalf("Snapshot lost the owner: %+v", s.Lines)
	}
	r, err := Restore(s)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lines()[0].OwnerRef != "S-7" || r.Lines()[1].OwnerRef != "" {
		t.Fatalf("Restore lost the owners: %+v", r.Lines())
	}
	s.Lines[0].OwnerRef = "-bad"
	if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("a malformed owner restored: %v", err)
	}
	q.Lines[0].OwnerRef = "-bad"
	if _, _, err := New(q, now); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("a malformed owner quoted: %v", err)
	}
}
