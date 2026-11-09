package shared

// CapacityBand is the coarse capacity pressure of the PromiseBook behind an
// offer's chosen window, read at quote time (ADR 0023). It is a signal, not a
// price: the network never computes a fee.
type CapacityBand string

// The three capacity bands.
const (
	BandAmple  CapacityBand = "AMPLE"
	BandTight  CapacityBand = "TIGHT"
	BandScarce CapacityBand = "SCARCE"
)

// The band thresholds, as percentages of the book's share used by provisional
// plus confirmed holds. They are domain constants, not PromisePolicy fields
// (ADR 0023): u < 60% is AMPLE, 60% <= u < 85% is TIGHT, u >= 85% is SCARCE.
const (
	TightFromPercent  = 60
	ScarceFromPercent = 85
)

// Valid reports whether b is one of the three bands.
func (b CapacityBand) Valid() bool {
	return b == BandAmple || b == BandTight || b == BandScarce
}

// CapacityBandFor classifies a book with used = provisional + confirmed holds
// and the given share. The comparison is on integers, so 0.59 never rounds up
// to TIGHT nor 0.84 to SCARCE. ok is false, and the band empty, when the book
// has no finite capacity (share <= 0) or used is negative: the field is then
// omitted, never an empty string.
func CapacityBandFor(used, share int) (band CapacityBand, ok bool) {
	if share <= 0 || used < 0 {
		return "", false
	}
	pct := int64(used) * 100
	switch {
	case pct >= int64(share)*ScarceFromPercent:
		return BandScarce, true
	case pct >= int64(share)*TightFromPercent:
		return BandTight, true
	default:
		return BandAmple, true
	}
}
