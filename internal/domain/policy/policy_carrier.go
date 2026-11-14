package policy

import (
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// Carrier-window bounds (ADR 0026). CarrierWindowHours is carried as a
// pointer: 0 is not a legal width, so nil is the omitted state and takes the
// default; the 0-means-omitted rule would work too, but a pointer keeps an
// explicitly-published width distinct from an absent one, like the
// calibration cap (ADR 0020).
const (
	MinCarrierWindowHours     = 1
	MaxCarrierWindowHours     = 96
	DefaultCarrierWindowHours = 24
)

// validateCarrierWindow checks the carrier window of a policy input: nil (or
// a snapshot persisted before ADR 0026 restores nil) takes the default, any
// value sits in 1..96.
func validateCarrierWindow(in Input) error {
	if in.CarrierWindowHours == nil {
		return nil
	}
	if *in.CarrierWindowHours < MinCarrierWindowHours || *in.CarrierWindowHours > MaxCarrierWindowHours {
		return shared.Invalid("carrierWindowHours must be between %d and %d", MinCarrierWindowHours, MaxCarrierWindowHours)
	}
	return nil
}

// carrierWindowOrDefaults resolves the width of a carrier promise's window.
func carrierWindowOrDefaults(in Input) int {
	if in.CarrierWindowHours == nil {
		return DefaultCarrierWindowHours
	}
	return *in.CarrierWindowHours
}

// CarrierWindowHours returns how wide the delivery window of a CARRIER_HANDOFF
// area's promise is: the carrier-promise Latest is the Earliest (arrival at
// the handoff node plus the carrier's effective transit) plus this width
// (ADR 0026). The default policy answers 24h.
func (p *Policy) CarrierWindowHours() time.Duration {
	return time.Duration(p.carrierWindow) * time.Hour
}
