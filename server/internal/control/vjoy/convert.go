// No build tag, unlike the Windows-only Backend: the range math is
// unit-testable anywhere.
package vjoy

import "math"

// toRaw maps -1..1 onto a vJoy axis's 0..max range (0 = center).
func toRaw(v float64, max int32) int32 {
	v = math.Max(-1, math.Min(1, v))
	return int32(math.Round((v + 1) / 2 * float64(max)))
}

// throttleToRaw follows the same two reverse-thrust conventions the
// SimConnect backend has. Signed: -1..1 across the whole axis, so idle sits
// at center and the MSFS binding must be a -100..100% throttle axis.
// Unsigned (default): the magnitude across the whole axis, idle at 0, with
// reverse carried separately by the reverse-hold button.
func throttleToRaw(v float64, signed bool, max int32) int32 {
	if signed {
		return toRaw(v, max)
	}
	return toRaw(2*math.Abs(v)-1, max)
}
