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

// Inverse returns the input that Apply maps to v — e.g. to show the sim's
// actual lever position on a phone control whose values go through this
// curve, so dragging from the shown position sends what produced it.
// Every dead-zone input maps to 0, so 0 inverts to 0.
func (c Curve) Inverse(v float64) float64 {
	sign := 1.0
	if v < 0 {
		sign, v = -1, -v
	}
	v = min(v, 1)
	if c.Expo > 0 {
		// (1-e)x + e*x^3 is monotonic on [0,1]; bisect rather than solve
		// the cubic.
		lo, hi := 0.0, 1.0
		for range 40 {
			mid := (lo + hi) / 2
			if (1-c.Expo)*mid+c.Expo*mid*mid*mid < v {
				lo = mid
			} else {
				hi = mid
			}
		}
		v = (lo + hi) / 2
	}
	if v > 0 && c.DeadZone < 1 {
		v = c.DeadZone + v*(1-c.DeadZone)
	}
	if c.Invert {
		sign = -sign
	}
	return sign * v
}
