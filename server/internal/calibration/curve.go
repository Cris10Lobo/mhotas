// Package calibration applies dead-zone, expo and inversion to axis values.
// It is server-side because it tunes the sim's response; correcting the
// phone's physical grip is the app's recenter gesture.
package calibration

type Curve struct {
	// DeadZone is the fraction (0..1) of deflection near center treated as zero.
	DeadZone float64 `json:"dead_zone"`

	// Expo blends between linear (0) and cubic (1).
	Expo float64 `json:"expo"`

	Invert bool `json:"invert"`
}

// Apply shapes a -1..1 input.
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
	// Rescale so the usable range starts at 0 with no jump after the dead zone.
	v = (v - c.DeadZone) / (1 - c.DeadZone)

	if c.Expo > 0 {
		v = (1-c.Expo)*v + c.Expo*v*v*v
	}

	return sign * v
}

// Inverse returns the input that Apply maps to v.
func (c Curve) Inverse(v float64) float64 {
	sign := 1.0
	if v < 0 {
		sign, v = -1, -v
	}
	v = min(v, 1)
	if c.Expo > 0 {
		// Monotonic on [0,1]; bisect instead of solving the cubic.
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
