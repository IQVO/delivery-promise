package clock

import (
	"testing"
	"time"
)

func TestSystemNowIsUTCAndMicrosecondPrecise(t *testing.T) {
	before := time.Now().UTC().Truncate(time.Microsecond)
	got := System{}.Now()
	if got.Location() != time.UTC || got.Nanosecond()%1000 != 0 || got.Before(before) || time.Since(got) > time.Minute {
		t.Fatalf("Now() = %v", got)
	}
}
