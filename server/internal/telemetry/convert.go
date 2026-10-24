package telemetry

import "mhotas/pkg/protocol"

// rawState mirrors simlink.AircraftState plus pause state, so this file stays free of the Windows-only simlink import.
type rawState struct {
	FlapHandleIndex        int32
	FlapNumHandlePositions int32
	GearPositionPercent    int32
	ThrottleLeverPercent   float32
	ParkingBrakeSet        int32
	StallWarning           int32
	OnGround               int32
	GroundVelocityKnots    float32
	SpoilersHandlePercent  float32
	SpoilersArmed          int32
	ElevatorTrim           float32
	AutopilotMaster        int32
	AutothrottleArmed      int32
	ManagedThrottleActive  int32
	AirspeedHold           int32
	MachHold               int32
	Paused                 bool
}

func toTelemetry(r rawState) protocol.Telemetry {
	return protocol.Telemetry{
		FlapHandleIndex:    int8(r.FlapHandleIndex),
		FlapHandleIndexMax: int8(r.FlapNumHandlePositions),
		GearPosition:       gearPosition(r.GearPositionPercent),
		ParkingBrakeSet:    r.ParkingBrakeSet != 0,
		ThrottlePercent:    percentToUnit(r.ThrottleLeverPercent),
		Paused:             r.Paused,
		StallWarning:       r.StallWarning != 0,
		OnGround:           r.OnGround != 0,
		GroundSpeedKnots:   r.GroundVelocityKnots,
		SpoilersHandle:     clampUnit(r.SpoilersHandlePercent / 100),
		SpoilersArmed:      r.SpoilersArmed != 0,
		ElevatorTrim:       clampUnit(r.ElevatorTrim),
		AutopilotMaster:    r.AutopilotMaster != 0,
		AutothrottleActive: autothrottleActive(r),
	}
}

// autothrottleActive guesses whether the autothrottle is driving the lever
// right now: Airbus managed thrust, or the A/T armed with a speed/Mach mode
// engaged. Armed alone isn't enough: a 737's A/T switch stays armed with
// the pilot on manual thrust, which would block vJoy resync all flight.
// ponytail: study-level aircraft may keep this in L:vars instead; add
// per-aircraft reads if one shows up on a real aircraft.
func autothrottleActive(r rawState) bool {
	return r.ManagedThrottleActive != 0 ||
		(r.AutothrottleArmed != 0 && (r.AirspeedHold != 0 || r.MachHold != 0))
}

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

func percentToUnit(pct float32) float32 { return clampUnit(pct / 100) }

func clampUnit(v float32) float32 {
	return max(-1, min(1, v))
}
