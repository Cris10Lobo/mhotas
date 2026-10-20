// This file has no build tag, unlike simconnect.go's windows-only Backend —
// the mapping below is pure arithmetic, unit-testable without SimConnect or
// Windows, mirroring how internal/telemetry splits its build-tag-free Sink
// interface from its Windows-only Source.
package simconnect

import "mhotas/internal/control"

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

// toSignedThrottleRaw applies control.RemapIdle and scales the result to
// SetThrottle's raw range. idlePoint=-1 is the fixed arm-toggle convention
// (see stockIdlePoint); any other value is the operator's tuned
// signed_throttle_idle (see throttleEx1Driver's SetSignedThrottleIdle).
func toSignedThrottleRaw(value, idlePoint float64, axisRange int32) int32 {
	return int32(control.RemapIdle(value, idlePoint) * float64(axisRange))
}
