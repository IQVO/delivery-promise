package http

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/views"
)

var calibrationAt = time.Date(2026, 10, 22, 19, 5, 0, 0, time.UTC)

func marshalOK(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func conformTo(t *testing.T, schema string, v any) {
	t.Helper()
	for _, e := range validateAgainst(t, schema, marshalOK(t, v)) {
		t.Errorf("%s: %s", schema, e)
	}
}

func offerWith(confidence *float64) views.Offer {
	return views.Offer{
		OfferID: "off-1", State: "QUOTED", GroupNo: 1, DestinationPostalPrefix: "787", Tier: "NEXT_DAY", Lines: []views.Line{{CatalogItemID: "ci-1", Quantity: 2}},
		Window:      views.Window{Earliest: calibrationAt, Latest: calibrationAt.Add(12 * time.Hour), TimeZone: "America/Chicago"},
		WitnessPath: views.Witness{NodeID: "N-1", PickupDepartureID: "d1", Legs: []string{}, DeliveryWaveID: "w1"},
		Basis:       "SCHEDULED", Confidence: confidence, PolicyVersion: "v1", QuotedAt: calibrationAt, ExpiresAt: calibrationAt, OrderByAt: calibrationAt,
	}
}

func termsWith(confidence *float64) views.Terms {
	return views.Terms{
		Tier: "NEXT_DAY", ShipBy: calibrationAt, DeliverByEarliest: calibrationAt, DeliverByLatest: calibrationAt, TimeZone: "America/Chicago",
		Basis: "MEASURED", Confidence: confidence, PolicyVersion: "v1",
	}
}

// The views a client receives conform to the OpenAPI schemas whether the
// confidence is known or unknown (omitted), and the schema pins its range.
func TestTheConfidenceFieldsConformToTheOpenAPISchemas(t *testing.T) {
	known := 0.973
	for _, c := range []*float64{&known, nil} {
		conformTo(t, "DeliveryOffer", offerWith(c))
		conformTo(t, "PromiseTerms", termsWith(c))
		conformTo(t, "EstimateOption", views.EstimateOption{
			Tier: "NEXT_DAY", Basis: "SCHEDULED", Confidence: c,
			Window: views.EstimateWindow{Start: calibrationAt, End: calibrationAt.Add(time.Hour), TimeZone: "America/Chicago"},
		})
	}
	conformTo(t, "PromisePolicy", views.Policy{
		PolicyVersion: "v1", Version: 1, OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, CapacitySharePercent: 100,
		TransitWindowDays: 28, TransitServiceLevelPercent: 90, MinTransitObservations: 20,
		TargetOnTimeProbabilityPercent: 95, MinCalibrationObservations: 50, CalibrationWindowDays: 28, MaxCalibrationBufferMinutes: 0,
		OrderDeadlines: []views.OrderDeadline{}, PublishedAt: calibrationAt,
	})
	schema := openAPISchemas(t)["Confidence"].(map[string]any)
	if schema["type"] != "number" || schema["minimum"] != 0 || schema["maximum"] != 1 || schema["multipleOf"] != 0.001 {
		t.Fatalf("Confidence schema = %v", schema)
	}
}

func TestTheSchemaCheckReportsAnUndeclaredCalibrationField(t *testing.T) {
	bad := marshalOK(t, offerWith(nil))
	bad = bad[:len(bad)-1] + `,"confidenceScore":0.9}`
	if errs := validateAgainst(t, "DeliveryOffer", bad); len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly the undeclared field", errs)
	}
}
