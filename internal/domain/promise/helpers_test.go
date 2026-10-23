package promise

import (
	"testing"
	"time"
)

func eq[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func eqTime(t *testing.T, name string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}
