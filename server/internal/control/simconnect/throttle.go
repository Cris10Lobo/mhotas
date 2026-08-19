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
