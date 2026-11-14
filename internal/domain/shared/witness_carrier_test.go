package shared

import "testing"

// The witness matrix of ADR 0026: a SCHEDULED or MEASURED witness ends on a
// delivery wave OR on a carrier (with its handoff node) - never both, never
// neither; a NOMINAL witness stays empty.
func TestWitnessValidateWaveOrCarrier(t *testing.T) {
	wave := Witness{NodeID: "N-AUS3", PickupDepartureID: "dep-1", DeliveryWaveID: "W-1"}
	carrier := Witness{NodeID: "N-AUS3", PickupDepartureID: "dep-1", CarrierID: "c-x", HandoffNodeID: "N-DAU1"}
	if err := wave.Validate(BasisScheduled); err != nil {
		t.Fatalf("a wave witness must validate: %v", err)
	}
	if err := carrier.Validate(BasisScheduled); err != nil {
		t.Fatalf("a carrier witness must validate: %v", err)
	}
	if err := carrier.Validate(BasisMeasured); err != nil {
		t.Fatalf("a MEASURED carrier witness carries the same path: %v", err)
	}
	both := carrier
	both.DeliveryWaveID = "W-1"
	if err := both.Validate(BasisScheduled); err == nil {
		t.Fatal("a witness naming both a wave and a carrier must be refused")
	}
	neither := Witness{NodeID: "N-AUS3", PickupDepartureID: "dep-1"}
	if err := neither.Validate(BasisScheduled); err == nil {
		t.Fatal("a witness naming neither a wave nor a carrier must be refused")
	}
	noPickup := carrier
	noPickup.PickupDepartureID = ""
	if err := noPickup.Validate(BasisScheduled); err == nil {
		t.Fatal("a carrier witness without a pickup departure must be refused")
	}
	half := Witness{NodeID: "N-AUS3", PickupDepartureID: "dep-1", CarrierID: "c-x"}
	if err := half.Validate(BasisMeasured); err == nil {
		t.Fatal("a carrier without its handoff node must be refused")
	}
	nodeOnly := Witness{NodeID: "N-AUS3", CarrierID: "c-x", HandoffNodeID: "N-DAU1"}
	if err := nodeOnly.Validate(BasisNominal); err == nil {
		t.Fatal("a NOMINAL witness carries no carrier fields")
	}
}

func TestWitnessCloneCopiesTheCarrierFields(t *testing.T) {
	w := Witness{NodeID: "N-AUS3", PickupDepartureID: "dep-1", Legs: []string{"dep-2"}, CarrierID: "c-x", HandoffNodeID: "N-DAU1"}
	got := w.Clone()
	got.CarrierID, got.HandoffNodeID = "c-other", "N-other"
	if w.CarrierID != "c-x" || w.HandoffNodeID != "N-DAU1" {
		t.Fatalf("Clone aliased the carrier fields: %+v", w)
	}
}
