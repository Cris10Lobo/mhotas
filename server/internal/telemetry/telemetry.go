// Package telemetry streams live aircraft state to the phone as TELEMETRY packets.
package telemetry

import (
	"log/slog"
	"math"

	"mhotas/pkg/protocol"
)

type Sink interface {
	SendTelemetry(t protocol.Telemetry) error
}

// controls is the part of Telemetry a pilot (or a test) sets; ground speed
// and stall warning change every frame and would flood the log.
type controls struct {
	flaps, flapsMax                 int8
	gear                            protocol.GearPosition
	throttle, spoilers, elevTrim    float64
	spoilersArmed, parkingBrake     bool
	paused, autopilot, autothrottle bool
}

func controlsOf(t protocol.Telemetry) controls {
	round := func(v float32) float64 { return math.Round(float64(v)*100) / 100 }
	return controls{
		flaps: t.FlapHandleIndex, flapsMax: t.FlapHandleIndexMax, gear: t.GearPosition,
		throttle: round(t.ThrottlePercent), spoilers: round(t.SpoilersHandle), elevTrim: round(t.ElevatorTrim),
		spoilersArmed: t.SpoilersArmed, parkingBrake: t.ParkingBrakeSet,
		paused: t.Paused, autopilot: t.AutopilotMaster, autothrottle: t.AutothrottleActive,
	}
}

// LogChanges logs the sim's control state whenever it changes, so the sim's
// side of a phone action can be checked from the server log alone.
func LogChanges(sink Sink, log *slog.Logger) Sink {
	return &changeLogger{sink: sink, log: log}
}

type changeLogger struct {
	sink Sink
	log  *slog.Logger
	last controls
	have bool
}

func (l *changeLogger) SendTelemetry(t protocol.Telemetry) error {
	if c := controlsOf(t); !l.have || c != l.last {
		l.last, l.have = c, true
		l.log.Info("sim", "flaps", c.flaps, "flapsMax", c.flapsMax, "gear", c.gear,
			"throttle", c.throttle, "spoilers", c.spoilers, "spoilersArmed", c.spoilersArmed,
			"elevTrim", c.elevTrim, "parkingBrake", c.parkingBrake, "paused", c.paused,
			"autopilot", c.autopilot, "autothrottle", c.autothrottle)
	}
	return l.sink.SendTelemetry(t)
}
