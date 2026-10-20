package telemetry

import (
	"testing"

	"mhotas/pkg/protocol"
)

func TestGearPosition(t *testing.T) {
	cases := []struct {
		pct  int32
		want protocol.GearPosition
	}{
		{pct: 0, want: protocol.GearUp},
		{pct: 100, want: protocol.GearDown},
		{pct: 50, want: protocol.GearTransit},
		{pct: -5, want: protocol.GearUp},    // clamps
		{pct: 105, want: protocol.GearDown}, // clamps
	}
	for _, c := range cases {
		if got := gearPosition(c.pct); got != c.want {
			t.Errorf("gearPosition(%v) = %v, want %v", c.pct, got, c.want)
		}
	}
}

func TestToTelemetryHapticFields(t *testing.T) {
	got := toTelemetry(rawState{
		StallWarning:        1,
		OnGround:            1,
		GroundVelocityKnots: 42.5,
	})
	if !got.StallWarning || !got.OnGround || got.GroundSpeedKnots != 42.5 {
		t.Errorf("toTelemetry haptic fields = %+v, want StallWarning/OnGround true, GroundSpeedKnots 42.5", got)
	}
}

func TestPercentToUnit(t *testing.T) {
	cases := []struct {
		pct  float32
		want float32
	}{
		{pct: 0, want: 0},
		{pct: 100, want: 1},
		{pct: -100, want: -1},
		{pct: 50, want: 0.5},
		{pct: 150, want: 1},   // clamps
		{pct: -150, want: -1}, // clamps
	}
	for _, c := range cases {
		if got := percentToUnit(c.pct); got != c.want {
			t.Errorf("percentToUnit(%v) = %v, want %v", c.pct, got, c.want)
		}
	}
}

func TestAutothrottleActive(t *testing.T) {
	cases := []struct {
		r    rawState
		want bool
	}{
		{rawState{AutothrottleArmed: 1}, false}, // armed, pilot on manual thrust
		{rawState{AutothrottleArmed: 1, AirspeedHold: 1}, true},
		{rawState{AutothrottleArmed: 1, MachHold: 1}, true},
		{rawState{AirspeedHold: 1}, false}, // speed mode without A/T: pitch-on-speed
		{rawState{ManagedThrottleActive: 1}, true},
	}
	for _, c := range cases {
		if got := autothrottleActive(c.r); got != c.want {
			t.Errorf("autothrottleActive(%+v) = %v, want %v", c.r, got, c.want)
		}
	}
}
