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
