package http

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/views"
)

func publishPolicy(t *testing.T, body string) (*fakeAPI, string) {
	t.Helper()
	api := &fakeAPI{}
	rec := do(NewRouter(&Server{API: api}, nil, ""), "POST", "/promise-policies", body)
	if rec.Code != 201 {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	return api, rec.Body.String()
}

// TestTheCalibrationPolicyFieldsRoundTrip posts the four ADR 0020 fields and
// reads them back in the published view.
func TestTheCalibrationPolicyFieldsRoundTrip(t *testing.T) {
	api, got := publishPolicy(t, `{"offerTtlMinutes":5,"holdTtlMinutes":20,"packagesPerOrder":2,"capacitySharePercent":50,`+
		`"targetOnTimeProbabilityPercent":90,"minCalibrationObservations":25,"calibrationWindowDays":14,"maxCalibrationBufferMinutes":180}`)
	in := api.policy
	if in.TargetOnTimeProbabilityPercent != 90 || in.MinCalibrationObservations != 25 || in.CalibrationWindowDays != 14 ||
		in.MaxCalibrationBufferMinutes == nil || *in.MaxCalibrationBufferMinutes != 180 {
		t.Fatalf("calibration input = %+v", in)
	}
	for _, want := range []string{`"targetOnTimeProbabilityPercent":90`, `"minCalibrationObservations":25`, `"calibrationWindowDays":14`, `"maxCalibrationBufferMinutes":180`} {
		if !strings.Contains(got, want) {
			t.Errorf("published view lacks %s: %s", want, got)
		}
	}
}

func TestOmittedCalibrationFieldsTakeTheirDefaultsButAZeroCapIsKept(t *testing.T) {
	api, got := publishPolicy(t, `{"offerTtlMinutes":5,"holdTtlMinutes":20,"packagesPerOrder":2,"capacitySharePercent":50}`)
	if api.policy.MaxCalibrationBufferMinutes != nil {
		t.Fatalf("an omitted cap must stay omitted, got %d", *api.policy.MaxCalibrationBufferMinutes)
	}
	for _, want := range []string{`"targetOnTimeProbabilityPercent":95`, `"minCalibrationObservations":50`, `"calibrationWindowDays":28`, `"maxCalibrationBufferMinutes":720`} {
		if !strings.Contains(got, want) {
			t.Errorf("default view lacks %s: %s", want, got)
		}
	}
	api, got = publishPolicy(t, `{"offerTtlMinutes":5,"holdTtlMinutes":20,"packagesPerOrder":2,"capacitySharePercent":50,"maxCalibrationBufferMinutes":0}`)
	if api.policy.MaxCalibrationBufferMinutes == nil || *api.policy.MaxCalibrationBufferMinutes != 0 || !strings.Contains(got, `"maxCalibrationBufferMinutes":0`) {
		t.Fatalf("an explicit 0 cap is a cap: %+v %s", api.policy.MaxCalibrationBufferMinutes, got)
	}
}

func TestConfidenceIsOmittedWhenUnknownAndNeverZero(t *testing.T) {
	known, unknown := 0.973, (*float64)(nil)
	zero := 0.0
	for name, c := range map[string]struct {
		in   *float64
		want string
	}{"known": {&known, `"confidence":0.973`}, "a real zero": {&zero, `"confidence":0`}, "unknown": {unknown, ""}} {
		raw, err := json.Marshal(views.Offer{OfferID: "off-1", Confidence: c.in, QuotedAt: time.Time{}})
		if err != nil {
			t.Fatal(err)
		}
		has := strings.Contains(string(raw), `"confidence"`)
		if c.want == "" && has || c.want != "" && !strings.Contains(string(raw), c.want) {
			t.Errorf("%s: %s", name, raw)
		}
	}
}
