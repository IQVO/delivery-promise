package shared_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

func TestCapacityBandBoundaries(t *testing.T) {
	cases := []struct {
		name        string
		used, share int
		want        shared.CapacityBand
	}{
		{"empty book", 0, 100, shared.BandAmple},
		{"0.59", 59, 100, shared.BandAmple},
		{"0.60", 60, 100, shared.BandTight},
		{"0.84", 84, 100, shared.BandTight},
		{"0.85", 85, 100, shared.BandScarce},
		{"full", 100, 100, shared.BandScarce},
		{"3 of 5 is exactly 0.60", 3, 5, shared.BandTight},
		{"2 of 5 is 0.40", 2, 5, shared.BandAmple},
		{"17 of 20 is exactly 0.85", 17, 20, shared.BandScarce},
		{"16 of 20 is 0.80", 16, 20, shared.BandTight},
		{"1 of 1", 1, 1, shared.BandScarce},
		{"0 of 1", 0, 1, shared.BandAmple},
		{"large share no overflow", 2147483647, 2147483647, shared.BandScarce},
		{"large share 59 percent", 1267000000, 2147483647, shared.BandAmple},
	}
	for _, c := range cases {
		got, ok := shared.CapacityBandFor(c.used, c.share)
		require.True(t, ok, c.name)
		require.Equal(t, c.want, got, c.name)
		require.True(t, got.Valid(), c.name)
	}
}

func TestCapacityBandOmitted(t *testing.T) {
	for _, c := range []struct {
		name        string
		used, share int
	}{
		{"no finite capacity", 0, 0},
		{"negative share", 3, -1},
		{"zero share with holds", 2, 0},
		{"negative used", -1, 10},
	} {
		got, ok := shared.CapacityBandFor(c.used, c.share)
		require.False(t, ok, c.name)
		require.Empty(t, got, c.name)
	}
}

func TestCapacityBandValid(t *testing.T) {
	require.False(t, shared.CapacityBand("").Valid())
	require.False(t, shared.CapacityBand("ample").Valid())
	require.Equal(t, 60, shared.TightFromPercent)
	require.Equal(t, 85, shared.ScarceFromPercent)
}
