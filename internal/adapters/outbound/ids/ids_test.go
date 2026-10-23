package ids

import (
	"strings"
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

func TestIdsAreValidPrefixedAndUnique(t *testing.T) {
	g := UUID{}
	o1, o2, p1 := g.NewOfferID(), g.NewOfferID(), g.NewPromiseID()
	if o1 == o2 || !strings.HasPrefix(string(o1), "off-") || !strings.HasPrefix(string(p1), "prm-") {
		t.Fatalf("ids = %s %s %s", o1, o2, p1)
	}
	if !shared.ValidID(string(o1)) || !shared.ValidID(string(p1)) {
		t.Fatalf("an id the domain would refuse: %s %s", o1, p1)
	}
}
