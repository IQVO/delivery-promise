package http

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

const consolidatedQuoteBody = `{"destinationPostalPrefix":"787","lines":[{"catalogItemId":"ci-1001","quantity":2}],"tiers":["STANDARD","CONSOLIDATED"],"consolidatedDeliveryDay":"2026-10-26"}`

func TestTheQuoteRouteDecodesTheChosenDeliveryDay(t *testing.T) {
	api := &fakeAPI{}
	h := NewRouter(&Server{API: api}, nil, "")
	if rec := do(h, "POST", "/delivery-quotes", consolidatedQuoteBody); rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	c := api.quote
	if c.ConsolidatedDeliveryDay != "2026-10-26" || len(c.Tiers) != 2 || c.Tiers[1] != shared.TierConsolidated {
		t.Fatalf("command = %+v", c)
	}
	if errs := validateAgainst(t, "QuoteRequest", consolidatedQuoteBody); len(errs) != 0 {
		t.Fatalf("the request does not conform to QuoteRequest: %v", errs)
	}
}

func TestAQuoteWithoutADayLeavesItEmpty(t *testing.T) {
	api := &fakeAPI{}
	h := NewRouter(&Server{API: api}, nil, "")
	do(h, "POST", "/delivery-quotes", `{"destinationPostalPrefix":"787","lines":[{"catalogItemId":"ci-1001","quantity":2}]}`)
	if api.quote.ConsolidatedDeliveryDay != "" || len(api.quote.Tiers) != 0 {
		t.Fatalf("a default quote asks for nothing extra: %+v", api.quote)
	}
}

func consolidatedOffer(eligible *bool) views.Offer {
	o := sampleOffer("off-c", "")
	o.Tier, o.ConsolidationEligible = "CONSOLIDATED", eligible
	return o
}

func TestAConsolidatedOfferConformsToTheOpenAPISchema(t *testing.T) {
	yes, no := true, false
	for name, eligible := range map[string]*bool{"true": &yes, "false": &no, "unknown": nil} {
		raw, err := json.Marshal(views.QuoteResult{Offers: []views.Offer{consolidatedOffer(eligible)}, Unavailable: []views.Unavailable{}})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(raw), `"consolidationEligible":`+name); got != (eligible != nil) {
			t.Fatalf("%s: wire form %s", name, raw)
		}
		for _, e := range validateAgainst(t, "QuoteResponse", string(raw)) {
			t.Errorf("%s: %s", name, e)
		}
	}
}

func TestAClassicOfferNeverCarriesConsolidationEligible(t *testing.T) {
	raw, err := json.Marshal(views.QuoteResult{Offers: []views.Offer{sampleOffer("off-1", "")}, Unavailable: []views.Unavailable{}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "consolidationEligible") {
		t.Fatalf("absent for every tier but CONSOLIDATED: %s", raw)
	}
}

func TestAConsolidatedPromiseTermsConformToTheOpenAPISchema(t *testing.T) {
	yes := true
	terms := views.Terms{Tier: "CONSOLIDATED", Basis: "SCHEDULED", PolicyVersion: "v0", ConsolidationEligible: &yes}
	raw, err := json.Marshal(terms)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range validateAgainst(t, "PromiseTerms", string(raw)) {
		t.Error(e)
	}
}

func TestTheEstimateRequestSchemaRefusesConsolidated(t *testing.T) {
	body := `{"destinationPostalPrefix":"787","items":[{"catalogItemId":"ci-1001","quantity":2}],"tiers":["CONSOLIDATED"]}`
	if got := strings.Join(validateAgainst(t, "EstimateRequest", body), "\n"); !strings.Contains(got, "CONSOLIDATED is not one of") {
		t.Fatalf("an estimate cannot ask for CONSOLIDATED: %q", got)
	}
}
