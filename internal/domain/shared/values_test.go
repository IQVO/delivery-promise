package shared

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTierRules(t *testing.T) {
	cases := []struct {
		tier    Tier
		days    int
		nominal bool
		rank    int
	}{
		{TierSameDay, 0, false, 0}, {TierNextDay, 1, false, 1}, {TierTwoDay, 2, false, 2}, {TierStandard, 7, true, 3},
	}
	for _, c := range cases {
		if !c.tier.Valid() || c.tier.MaxCalendarDays() != c.days || c.tier.AllowsNominal() != c.nominal || c.tier.Rank() != c.rank {
			t.Errorf("tier %s: days=%d nominal=%v rank=%d", c.tier, c.tier.MaxCalendarDays(), c.tier.AllowsNominal(), c.tier.Rank())
		}
	}
	if Tier("EXPRESS").Valid() || Tier("EXPRESS").Rank() != -1 || Tier("EXPRESS").SlowerOrEqual() != nil {
		t.Fatal("an unknown tier is invalid")
	}
	if got := TierTwoDay.SlowerOrEqual(); len(got) != 2 || got[0] != TierTwoDay || got[1] != TierStandard {
		t.Fatalf("SlowerOrEqual(TWO_DAY) = %v", got)
	}
	all := AllTiers()
	all[0] = "X"
	if AllTiers()[0] != TierSameDay {
		t.Fatal("AllTiers must return a copy")
	}
}

func TestBasisValid(t *testing.T) {
	if !BasisScheduled.Valid() || !BasisNominal.Valid() || Basis("GUESS").Valid() || Basis("").Valid() {
		t.Fatal("only SCHEDULED and NOMINAL are bases")
	}
}

func TestValidateLines(t *testing.T) {
	ok := []Line{{"ci-1", 1}, {"ci-2", MaxQuantity}}
	if err := ValidateLines(ok); err != nil {
		t.Fatalf("valid lines refused: %v", err)
	}
	many := make([]Line, MaxLines)
	for i := range many {
		many[i] = Line{CatalogItemID: "i" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26)), Quantity: 1}
	}
	if err := ValidateLines(many); err != nil {
		t.Fatalf("MaxLines lines refused: %v", err)
	}
	bad := map[string][]Line{
		"none":         nil,
		"too many":     append(many, Line{"zzz", 1}),
		"bad id":       {{"-x", 1}},
		"duplicate":    {{"a", 1}, {"a", 2}},
		"zero qty":     {{"a", 0}},
		"negative qty": {{"a", -1}},
		"qty ceiling":  {{"a", MaxQuantity + 1}},
	}
	for name, lines := range bad {
		if err := ValidateLines(lines); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
}

func TestSameLines(t *testing.T) {
	a := []Line{{"a", 1}, {"b", 2}}
	cases := []struct {
		name string
		b    []Line
		want bool
	}{
		{"same order", []Line{{"a", 1}, {"b", 2}}, true},
		{"other order", []Line{{"b", 2}, {"a", 1}}, true},
		{"other quantity", []Line{{"a", 1}, {"b", 3}}, false},
		{"other item", []Line{{"a", 1}, {"c", 2}}, false},
		{"shorter", []Line{{"a", 1}}, false},
	}
	for _, c := range cases {
		if SameLines(a, c.b) != c.want {
			t.Errorf("%s: want %v", c.name, c.want)
		}
	}
}

func TestValidPostalPrefix(t *testing.T) {
	for _, s := range []string{"7", "787", "SW1A", "0123456789"} {
		if !ValidPostalPrefix(s) {
			t.Errorf("%q must be valid", s)
		}
	}
	for _, s := range []string{"", "01234567890", "78 7", "78-7", "7é", "7/", "7:", "7@", "7[", "7`", "7{"} {
		if ValidPostalPrefix(s) {
			t.Errorf("%q must be invalid", s)
		}
	}
}

func TestWindowValidate(t *testing.T) {
	t0 := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	if err := (Window{Earliest: t0, Latest: t0}).Validate(); err != nil {
		t.Fatalf("a zero-length window is legal: %v", err)
	}
	if err := (Window{Earliest: t0, Latest: t0.Add(time.Hour), TimeZone: "America/Chicago"}).Validate(); err != nil {
		t.Fatalf("a window with a zone is legal: %v", err)
	}
	for name, w := range map[string]Window{
		"inverted": {Earliest: t0.Add(time.Hour), Latest: t0}, "no earliest": {Latest: t0}, "no latest": {Earliest: t0},
		"bad zone": {Earliest: t0, Latest: t0, TimeZone: "America/Chi cago"},
	} {
		if err := w.Validate(); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestWindowUTCKeepsTheZone(t *testing.T) {
	cdt := time.FixedZone("CDT", -5*3600)
	w := Window{Earliest: time.Date(2026, 10, 31, 9, 0, 0, 0, cdt), Latest: time.Date(2026, 10, 31, 21, 0, 0, 0, cdt), TimeZone: "America/Chicago"}
	got := w.UTC()
	want := Window{Earliest: time.Date(2026, 10, 31, 14, 0, 0, 0, time.UTC), Latest: time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC), TimeZone: "America/Chicago"}
	if got != want || got.Earliest.Location() != time.UTC || got.Latest.Location() != time.UTC {
		t.Fatalf("UTC() = %+v, want %+v", got, want)
	}
}

func TestValidTimeZone(t *testing.T) {
	long := strings.Repeat("A", MaxTimeZone)
	for _, s := range []string{"UTC", "America/Chicago", "America/Argentina/Buenos_Aires", "Etc/GMT+3", "Etc/GMT-14", "America/Port-au-Prince", "Z", long} {
		if !ValidTimeZone(s) {
			t.Errorf("%q must be valid", s)
		}
	}
	for _, s := range []string{"", long + "A", "/America", "America/", "America/Chi cago", "America:Chicago", "Europe/Zürich", "America.Chicago", "a@b"} {
		if ValidTimeZone(s) {
			t.Errorf("%q must be invalid", s)
		}
	}
}

func TestWitnessValidate(t *testing.T) {
	sched := Witness{NodeID: "n1", PickupDepartureID: "d1", Legs: []string{"d2"}, DeliveryWaveID: "w1"}
	if err := sched.Validate(BasisScheduled); err != nil {
		t.Fatalf("scheduled witness refused: %v", err)
	}
	if err := (Witness{NodeID: "n1"}).Validate(BasisNominal); err != nil {
		t.Fatalf("nominal witness refused: %v", err)
	}
	bad := []struct {
		name  string
		w     Witness
		basis Basis
	}{
		{"bad node", Witness{NodeID: "-"}, BasisNominal},
		{"nominal with pickup", Witness{NodeID: "n1", PickupDepartureID: "d1"}, BasisNominal},
		{"nominal with wave", Witness{NodeID: "n1", DeliveryWaveID: "w1"}, BasisNominal},
		{"nominal with legs", Witness{NodeID: "n1", Legs: []string{"d"}}, BasisNominal},
		{"scheduled without pickup", Witness{NodeID: "n1", DeliveryWaveID: "w1"}, BasisScheduled},
		{"scheduled without wave", Witness{NodeID: "n1", PickupDepartureID: "d1"}, BasisScheduled},
		{"scheduled bad leg", Witness{NodeID: "n1", PickupDepartureID: "d1", DeliveryWaveID: "w1", Legs: []string{"!"}}, BasisScheduled},
	}
	for _, c := range bad {
		if err := c.w.Validate(c.basis); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
	c := sched.Clone()
	c.Legs[0] = "x"
	if sched.Legs[0] != "d2" {
		t.Fatal("Clone must copy the legs")
	}
}
