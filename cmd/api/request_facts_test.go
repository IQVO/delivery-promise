package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/requestfacts"
)

func capture() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func TestRequestFactsAreOffUnlessExplicitlyEnabled(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "  ": false, "false": false, "0": false, "FALSE": false, "true": true, "TRUE": true, "1": true, " true ": true} {
		lg, buf := capture()
		if got := requestFactsEnabled(raw, lg); got != want {
			t.Errorf("DELIVERY_REQUEST_FACTS_ENABLED=%q = %v, want %v", raw, got, want)
		}
		if buf.Len() != 0 {
			t.Errorf("%q logged %q: a valid value is silent", raw, buf)
		}
	}
	// a typo never switches an extra write on
	for _, raw := range []string{"yes", "on", "enabled", "tru"} {
		lg, buf := capture()
		if requestFactsEnabled(raw, lg) {
			t.Errorf("%q switched the facts on", raw)
		}
		if !strings.Contains(buf.String(), "DELIVERY_REQUEST_FACTS_ENABLED") {
			t.Errorf("%q: no warning naming the variable: %q", raw, buf)
		}
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestRequestFactsConfigDefaultsAndOverrides(t *testing.T) {
	lg, buf := capture()
	if got := requestFactsConfig(env(nil), lg); got.Capacity != 1000 || got.FlushInterval != time.Second || buf.Len() != 0 {
		t.Fatalf("defaults = %+v, log %q", got, buf)
	}
	got := requestFactsConfig(env(map[string]string{envRequestFactsQueueSize: " 250 ", envRequestFactsFlushInterval: "500ms"}), lg)
	if got.Capacity != 250 || got.FlushInterval != 500*time.Millisecond {
		t.Fatalf("overrides = %+v", got)
	}
	if got := requestFactsConfig(env(map[string]string{envRequestFactsFlushInterval: "3"}), lg); got.FlushInterval != 3*time.Second {
		t.Fatalf("plain seconds = %+v", got)
	}
	if got := requestFactsConfig(env(map[string]string{envRequestFactsQueueSize: "100000"}), lg); got.Capacity != requestfacts.MaxCapacity {
		t.Fatalf("the maximum is accepted: %+v", got)
	}
	if got := requestFactsConfig(env(map[string]string{envRequestFactsQueueSize: "1"}), lg); got.Capacity != 1 {
		t.Fatalf("the minimum is accepted: %+v", got)
	}
}

func TestRequestFactsConfigRefusesWhatIsOutOfRangeWithAWarning(t *testing.T) {
	for _, raw := range []string{"0", "-5", "100001", "many", "1.5"} {
		lg, buf := capture()
		got := requestFactsConfig(env(map[string]string{envRequestFactsQueueSize: raw, envRequestFactsFlushInterval: "never"}), lg)
		if got.Capacity != requestfacts.DefaultCapacity || got.FlushInterval != requestfacts.DefaultFlushInterval {
			t.Errorf("%q: config = %+v, want the defaults", raw, got)
		}
		if !strings.Contains(buf.String(), "DELIVERY_REQUEST_FACTS_QUEUE_SIZE") || !strings.Contains(buf.String(), "DELIVERY_REQUEST_FACTS_FLUSH_INTERVAL") {
			t.Errorf("%q: warnings = %q", raw, buf)
		}
	}
}

func TestStartRequestFactsIsANoOpWhileOff(t *testing.T) {
	lg, _ := capture()
	rec, w, err := startRequestFacts(nil, nil, env(nil), lg)
	if rec != nil || w != nil || err != nil {
		t.Fatalf("off: recorder %v worker %v err %v; want nothing started", rec, w, err)
	}
}
