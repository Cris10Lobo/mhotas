// No build tag, unlike the Windows-only Backend: the range math is
// unit-testable anywhere.
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

// throttleToRaw follows the same two reverse-thrust conventions the
// SimConnect backend has, with the same idle remap (control.RemapIdle).
// Signed: -1..1 across the whole axis with idle at idlePoint — the
// operator's signed_throttle_idle, since where idle falls on the axis is
// set by the aircraft and the MSFS binding (e.g. a detented airliner whose
// idle is a quarter of the way up). Unsigned (default): the magnitude
// across the whole axis, idle at 0, with reverse carried separately by the
// reverse-hold button. invert reverses the final axis position (see
// control.SignedThrottleSetter.SetThrottleInvert).
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
