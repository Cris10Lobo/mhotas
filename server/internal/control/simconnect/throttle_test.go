package simconnect

import "testing"

func TestToThrottleRaw(t *testing.T) {
	cases := []struct {
		p    float64
		want int32
	}{
		{p: 0, want: -16383},  // idle
		{p: 1, want: 16383},   // full power
		{p: 0.5, want: 0},     // midpoint, NOT idle — the whole point of this mapping
		{p: -1, want: -16383}, // clamps
		{p: 2, want: 16383},   // clamps
	}
	for _, c := range cases {
		if got := toThrottleRaw(c.p, 16383); got != c.want {
			t.Errorf("toThrottleRaw(%v) = %v, want %v", c.p, got, c.want)
		}
	}
}

func TestToSignedThrottleRaw(t *testing.T) {
	cases := []struct {
		value float64
		want  int32
	}{
		{value: 0, want: -8191},     // idle lands at signedThrottleIdlePoint, not raw 0
		{value: 1, want: 16383},     // TOGA
		{value: -1, want: -16383},   // full reverse
		{value: 0.5, want: 4095},    // halfway to TOGA from idle
		{value: -0.5, want: -12287}, // halfway to full reverse from idle
		{value: 2, want: 16383},     // clamps
		{value: -2, want: -16383},   // clamps
	}
	for _, c := range cases {
		if got := toSignedThrottleRaw(c.value, 16383); got != c.want {
			t.Errorf("toSignedThrottleRaw(%v) = %v, want %v", c.value, got, c.want)
		}
	}
}
