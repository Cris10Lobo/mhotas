// This file has no build tag, unlike source_windows.go's Windows-only
// Source — the mapping below is pure arithmetic, unit-testable without
// SimConnect or Windows, mirroring how internal/control/simconnect splits
// its build-tag-free throttle.go from its Windows-only Backend.
package telemetry

import "mhotas/pkg/protocol"

// rawState is Source's build-tag-free input to toTelemetry — a plain
// mirror of simlink.AircraftState plus pause state (from
// simlink.Client.Paused()), kept as its own type rather than importing
// simlink's Windows-only package here.
type rawState struct {
	FlapHandleIndex        int32
	FlapNumHandlePositions int32
	GearPositionPercent    int32
	ThrottleLeverPercent   int32
	ParkingBrakeSet        int32
	Paused                 bool
	AirspeedKnots          float64
	AltitudeFeet           float64
	HeadingDegrees         float64
	VerticalSpeedFpm       float64
}

func toTelemetry(r rawState) protocol.Telemetry {
	return protocol.Telemetry{
		FlapHandleIndex:    int8(r.FlapHandleIndex),
		FlapHandleIndexMax: int8(r.FlapNumHandlePositions),
		GearPosition:       gearPosition(r.GearPositionPercent),
		ParkingBrakeSet:    r.ParkingBrakeSet != 0,
		ThrottlePercent:    percentToUnit(r.ThrottleLeverPercent),
		Paused:             r.Paused,
		AirspeedKnots:      float32(r.AirspeedKnots),
		AltitudeFeet:       float32(r.AltitudeFeet),
		HeadingDegrees:     float32(r.HeadingDegrees),
		VerticalSpeedFpm:   float32(r.VerticalSpeedFpm),
	}
}

// gearPosition maps GEAR POSITION's 0..100 percent to the protocol's
// coarse Up/Down/Transit enum.
func gearPosition(pct int32) protocol.GearPosition {
	switch {
	case pct <= 0:
		return protocol.GearUp
	case pct >= 100:
		return protocol.GearDown
	default:
		return protocol.GearTransit
	}
}

// percentToUnit maps a SimConnect -100..100 percent reading to the wire
// protocol's -1..1 range, clamped defensively in case a given aircraft's
// model ever reports outside that span.
func percentToUnit(pct int32) float32 {
	v := float32(pct) / 100
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}
