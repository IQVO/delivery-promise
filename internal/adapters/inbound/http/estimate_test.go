package http

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// updateGolden regenerates testdata/estimate_response.golden.json:
//
//	go test ./internal/adapters/inbound/http -run TestEstimateResponseGolden -update
var updateGolden = flag.Bool("update", false, "rewrite the HTTP golden files")

const estimateBody = `{"destinationPostalPrefix":"787","items":[{"catalogItemId":"ci-1001","quantity":2},{"catalogItemId":"ci-404","quantity":1}],"tiers":["NEXT_DAY","TWO_DAY","STANDARD"]}`

// sampleEstimate is the fixed answer the golden pins: one item with two
// options (one without orderByAt) and a per-tier constraint, one item with an
// item-wide constraint.
func sampleEstimate() views.EstimateResult {
	cdt := time.FixedZone("CDT", -5*3600)
	orderBy := time.Date(2026, 10, 22, 16, 25, 0, 0, cdt).UTC()
	return views.EstimateResult{
		ComputedAt:              time.Date(2026, 10, 22, 14, 5, 0, 0, cdt).UTC(),
		DestinationPostalPrefix: "787",
		Estimates: []views.Estimate{
			{
				CatalogItemID: "ci-1001", Quantity: 2,
				Options: []views.EstimateOption{
					{Tier: "NEXT_DAY", Basis: "SCHEDULED", OrderByAt: &orderBy, Window: views.EstimateWindow{
						Start: time.Date(2026, 10, 23, 9, 0, 0, 0, cdt).UTC(), End: time.Date(2026, 10, 23, 21, 0, 0, 0, cdt).UTC(), TimeZone: "America/Chicago",
					}},
					{Tier: "STANDARD", Basis: "NOMINAL", Window: views.EstimateWindow{
						Start: time.Date(2026, 10, 26, 9, 0, 0, 0, cdt).UTC(), End: time.Date(2026, 10, 26, 21, 0, 0, 0, cdt).UTC(), TimeZone: "America/Chicago",
					}},
				},
				Constraints: []views.EstimateConstraint{{Tier: "TWO_DAY", Code: "NODE_CAPACITY_EXHAUSTED"}},
			},
			{
				CatalogItemID: "ci-404", Quantity: 1, Options: []views.EstimateOption{},
				Constraints: []views.EstimateConstraint{{Code: "ITEM_QUANTITY_NOT_AVAILABLE"}},
			},
		},
	}
}

// estimateResponse posts estimateBody (with an Idempotency-Key, which the
// route ignores) to a router over a fake answering sampleEstimate.
func estimateResponse(t *testing.T, api *fakeAPI) (int, string, http.Header) {
	t.Helper()
	api.estimated = sampleEstimate()
	req := httptest.NewRequest(http.MethodPost, "/delivery-estimates", strings.NewReader(estimateBody))
	req.Header.Set("Idempotency-Key", "listing-view-1")
	rec := httptest.NewRecorder()
	NewRouter(&Server{API: api}, nil, "").ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), rec.Header()
}

func TestTheEstimateRouteDecodesItsBodyIntoTheCommand(t *testing.T) {
	api := &fakeAPI{}
	status, _, hdr := estimateResponse(t, api)
	if status != http.StatusOK || hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("status %d content type %q", status, hdr.Get("Content-Type"))
	}
	if hdr.Get("Idempotency-Replayed") != "" {
		t.Fatal("a safe route never answers Idempotency-Replayed")
	}
	c := api.estimate
	if c.DestinationPostalPrefix != "787" || len(c.Items) != 2 || c.Items[1].CatalogItemID != "ci-404" || c.Items[0].Quantity != 2 {
		t.Fatalf("command = %+v", c)
	}
	if len(c.Tiers) != 3 || c.Tiers[0] != shared.TierNextDay || c.Tiers[2] != shared.TierStandard {
		t.Fatalf("tiers = %v", c.Tiers)
	}
}

// The response bytes are pinned: field names, order, omissions (orderByAt
// and tier), and UTC instants.
func TestEstimateResponseGolden(t *testing.T) {
	_, body, _ := estimateResponse(t, &fakeAPI{})
	path := filepath.Join("testdata", "estimate_response.golden.json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if body != string(want) {
		t.Fatalf("POST /delivery-estimates drifted from %s; review and re-run with -update.\ngot:  %s\nwant: %s", path, body, want)
	}
}

func TestEstimateRefusals(t *testing.T) {
	h := NewRouter(&Server{API: &fakeAPI{err: shared.Invalid("an estimate needs between 1 and 50 items")}}, nil, "")
	if rec := do(h, "POST", "/delivery-estimates", `{"destinationPostalPrefix":"787","items":[]}`); rec.Code != 422 || !strings.Contains(rec.Body.String(), "validation-failed") {
		t.Errorf("a refused command: %d %s", rec.Code, rec.Body)
	}
	h = NewRouter(&Server{API: &fakeAPI{}}, nil, "")
	if rec := do(h, "POST", "/delivery-estimates", `{"destinationPostalPrefix":"787","lines":[]}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "malformed-request") {
		t.Errorf("an unknown field: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/delivery-estimates", ``); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}
}
