// This file has no build tag, unlike simconnect.go's windows-only Backend —
// the mapping below is pure arithmetic, unit-testable without SimConnect or
// Windows, mirroring how internal/telemetry splits its build-tag-free Sink
// interface from its Windows-only Source.
package simconnect

// toThrottleRaw maps a calibrated throttle value (0..1, 0=idle) to
// AXIS_THROTTLE_SET's raw range. Unlike elevator/aileron/rudder — naturally
// centered controls where raw 0 already means neutral — AXIS_THROTTLE_SET
// uses the same signed -16383..16383 range as those, but with raw 0 at the
// range's MIDPOINT, not idle. Confirmed against the official SimConnect SDK
// docs' actual behavior (not just its misleading "(0-16383)" doc text) and
// a working example from an Asobo developer on the MSFS DevSupport forum
// (devsupport.flightsimulator.com/t/throttle-set-event-ids-explanation/4571):
// idle sits at the range's negative end, full power at the positive end.
// Verified live: sending raw 0 for "idle" consistently produced ~50-58%
// power (not idle) on both a Cessna 172 and a Boeing-like jet — this
// mapping is what actually reaches true idle at p=0.
func toThrottleRaw(p float64, axisRange int32) int32 {
	if p > 1 {
		p = 1
	}
	if p < 0 {
		p = 0
	}
	return int32((2*p - 1) * float64(axisRange))
}

// signedThrottleIdlePoint is where "idle" actually sits on the signed
// throttle scale (see throttleEx1Driver's signed mode) — not 0. flyPad's own
// throttle-calibration docs (docs.flybywiresim.com/aircraft/common/
// flypados3/throttle-calibration) document -0.5 as idle's default position
// on that same -1..1 scale (full reverse -1, reverse idle -0.7, climb 0,
// flex/MCT 0.5, TOGA +1) for an axis with no per-controller calibration
// entry — which a synthetic SimConnect axis always is, mhotas isn't a real
// assigned MSFS controller. Confirmed live: sending our raw 0 for "idle"
// landed on the climb detent (their 0.0) at ~75% power, exactly this offset.
//
// ponytail: hardcoded to flyPad's documented default rather than a
// config.json field — add one if a real aircraft turns out to need a
// different idle point.
const signedThrottleIdlePoint = -0.5

// toSignedThrottleRaw remaps this project's own idle=0/reverse<0/forward>0
// convention onto signedThrottleIdlePoint before it reaches SetThrottle:
// idle..full-power (0..1) stretches across idle..TOGA
// (signedThrottleIdlePoint..1), and full-reverse..idle (-1..0) stretches
// across full-reverse..idle (-1..signedThrottleIdlePoint) — piecewise linear
// around the one point that isn't 0, same shape as toThrottleRaw's own fix
// for the (different) stock-aircraft idle offset above.
func toSignedThrottleRaw(value float64, axisRange int32) int32 {
	if value > 1 {
		value = 1
	}
	if value < -1 {
		value = -1
	}
	var raw float64
	if value >= 0 {
		raw = signedThrottleIdlePoint + value*(1-signedThrottleIdlePoint)
	} else {
		raw = signedThrottleIdlePoint + value*(signedThrottleIdlePoint+1)
	}
	return int32(raw * float64(axisRange))
}
