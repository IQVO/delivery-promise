package http

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/views"
)

func sampleOffer(id, band string) views.Offer {
	at := time.Date(2026, 10, 22, 19, 5, 0, 0, time.UTC)
	return views.Offer{
		OfferID: id, State: "QUOTED", GroupNo: 1, DestinationPostalPrefix: "787", Tier: "NEXT_DAY",
		Lines:       []views.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
		Window:      views.Window{Earliest: at.Add(18 * time.Hour), Latest: at.Add(30 * time.Hour), TimeZone: "America/Chicago"},
		WitnessPath: views.Witness{NodeID: "N-AUS3", PickupDepartureID: "dep-AUS3-1800", Legs: []string{"dep-SAU1-DAU1-2200"}, DeliveryWaveID: "W-DAU1-1023-A"},
		Basis:       "SCHEDULED", PolicyVersion: "v0", QuotedAt: at, ExpiresAt: at.Add(10 * time.Minute), OrderByAt: at.Add(3 * time.Hour), CapacityBand: band,
	}
}

// capacityBand (ADR 0023) is declared by the OpenAPI schema: every band
// validates, the field is omitted (never empty) when unknown, and a value
// outside the vocabulary is refused by the checker.
func TestAQuoteResponseWithABandConformsToTheOpenAPISchema(t *testing.T) {
	for _, band := range []string{"AMPLE", "TIGHT", "SCARCE"} {
		raw, err := json.Marshal(views.QuoteResult{Offers: []views.Offer{sampleOffer("off-1", band)}, Unavailable: []views.Unavailable{}})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"capacityBand":"`+band+`"`) {
			t.Fatalf("the band is missing from %s", raw)
		}
		for _, e := range validateAgainst(t, "QuoteResponse", string(raw)) {
			t.Errorf("%s: %s", band, e)
		}
	}
}

func TestAQuoteOfferWithoutABandOmitsTheField(t *testing.T) {
	raw, err := json.Marshal(views.QuoteResult{Offers: []views.Offer{sampleOffer("off-1", "")}, Unavailable: []views.Unavailable{}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "capacityBand") {
		t.Fatalf("an unknown band is omitted, never empty: %s", raw)
	}
	for _, e := range validateAgainst(t, "QuoteResponse", string(raw)) {
		t.Error(e)
	}
}

func TestTheSchemaRefusesABandOutsideTheVocabulary(t *testing.T) {
	raw, err := json.Marshal(views.QuoteResult{Offers: []views.Offer{sampleOffer("off-1", "FULL")}, Unavailable: []views.Unavailable{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(validateAgainst(t, "QuoteResponse", string(raw)), "\n"); !strings.Contains(got, "FULL is not one of") {
		t.Fatalf("drift not reported: %q", got)
	}
}
