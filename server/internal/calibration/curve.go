// Package calibration shapes raw normalized axis values into what the
// simulator should actually see: dead-zone, response curve, and inversion.
// This lives server-side, not phone-side, because it's about how the
// *simulator* responds, not about correcting the phone's physical
// orientation — that's the phone's own recenter gesture's job.
package calibration

// Curve describes per-axis response shaping.
type Curve struct {
	// DeadZone is the fraction (0..1) of full deflection near center that's
	// treated as exactly zero. 0 disables the dead-zone.
	DeadZone float64 `json:"dead_zone"`

	// Expo blends the shaped output between linear (0) and cubic (1),
	// giving finer control near center while still reaching full deflection
	// at the extremes. 0 disables the curve (linear response).
	Expo float64 `json:"expo"`

	Invert bool `json:"invert"`
}

// Apply shapes a normalized input value (-1..1 for every axis, including
// throttle, where negative is reverse thrust). Sign is preserved through
// dead-zone and expo shaping.
func (c Curve) Apply(value float64) float64 {
	v := value
	if c.Invert {
		v = -v
	}

	sign := 1.0
	if v < 0 {
		sign = -1.0
		v = -v
	}

	if c.DeadZone >= 1 || v <= c.DeadZone {
		return 0
	}
	// Rescale so [DeadZone, 1] maps to [0, 1] instead of leaving a gap at
	// the start of the usable range.
	v = (v - c.DeadZone) / (1 - c.DeadZone)

	if c.Expo > 0 {
		v = (1-c.Expo)*v + c.Expo*v*v*v
	}

	return sign * v
}
