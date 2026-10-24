// No build tag, unlike Backend, so the range math is testable on any OS.
package vjoy

import (
	"math"

	"mhotas/internal/control"
)

// toRaw maps -1..1 onto a vJoy axis's 0..max range (0 = center).
func toRaw(v float64, max int32) int32 {
	v = math.Max(-1, math.Min(1, v))
	return int32(math.Round((v + 1) / 2 * float64(max)))
}

// throttleToRaw: signed spans -1..1 over the whole axis with idle at
// idlePoint (the aircraft and MSFS binding decide where idle falls).
// Unsigned maps the magnitude with idle at 0; reverse is the separate
// reverse-hold button. invert mirrors the final axis position, after the
// idle remap.
func throttleToRaw(v float64, signed bool, idlePoint float64, invert bool, max int32) int32 {
	r := control.RemapIdle(math.Abs(v), -1)
	if signed {
		r = control.RemapIdle(v, idlePoint)
	}
	if invert {
		r = -r
	}
	return toRaw(r, max)
}
