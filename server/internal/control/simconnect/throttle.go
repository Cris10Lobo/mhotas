// This file has no build tag, unlike simconnect.go's windows-only Backend —
// the mapping below is pure arithmetic, unit-testable without SimConnect or
// Windows, mirroring how internal/telemetry splits its build-tag-free Sink
// interface from its Windows-only Source.
package simconnect

// stockIdlePoint is where THROTTLE_AXIS_SET_EX1's raw range puts idle under
// the arm-toggle convention (throttleEx1Driver's non-signed mode): idle sits
// at the range's negative end, not raw 0 like elevator/aileron/rudder.
// Confirmed against the official SimConnect SDK docs' actual behavior (not
// just its misleading "(0-16383)" doc text) and a working example from an
// Asobo developer on the MSFS DevSupport forum
// (devsupport.flightsimulator.com/t/throttle-set-event-ids-explanation/4571).
// Verified live: sending raw 0 for "idle" consistently produced ~50-58%
// power (not idle) on both a Cessna 172 and a Boeing-like jet.
//
// This is a fixed, documented SDK fact — unlike toSignedThrottleRaw's own
// idlePoint parameter, which is a per-aircraft convention this project can't
// know in advance and never guesses at.
const stockIdlePoint = -1

// toSignedThrottleRaw remaps this project's own idle=0/reverse<0/forward>0
// convention onto idlePoint before it reaches SetThrottle — idle..full-power
// (0..1) stretches across idlePoint..1, and full-reverse..idle (-1..0)
// stretches across -1..idlePoint. idlePoint=0 is the identity mapping (raw
// signed value passed straight through, no remap at all); idlePoint=-1 is
// the fixed arm-toggle convention (see stockIdlePoint) — any other value
// only ever comes from whatever the operator or the phone's Settings screen
// set it to (see throttleEx1Driver's SetSignedThrottleIdle), since this
// project has no way to know where "idle" actually falls on a given
// aircraft's own signed throttle scale.
func toSignedThrottleRaw(value, idlePoint float64, axisRange int32) int32 {
	if value > 1 {
		value = 1
	}
	if value < -1 {
		value = -1
	}
	var raw float64
	if value >= 0 {
		raw = idlePoint + value*(1-idlePoint)
	} else {
		raw = idlePoint + value*(idlePoint+1)
	}
	return int32(raw * float64(axisRange))
}
