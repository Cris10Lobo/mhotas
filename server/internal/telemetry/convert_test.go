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

func TestPercentToUnit(t *testing.T) {
	cases := []struct {
		pct  int32
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
