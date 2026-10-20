package vjoy

import "testing"

func TestThrottleToRaw(t *testing.T) {
	const max = 32767
	cases := []struct {
		v      float64
		signed bool
		idle   float64
		want   int32
	}{
		{v: 0, signed: false, want: 0},
		{v: 1, signed: false, want: max},
		{v: -1, signed: false, want: max}, // full reverse = full magnitude
		{v: 0.5, signed: false, want: 16384},
		{v: 0, signed: true, want: 16384},
		{v: -1, signed: true, want: 0},
		{v: 1, signed: true, want: max},
		{v: 2, signed: true, want: max}, // clamps
		// Detented airliner with idle a quarter of the way up the axis:
		// phone idle must land there, not at center (which it read as ~75%).
		{v: 0, signed: true, idle: -0.5, want: 8192},
		{v: 1, signed: true, idle: -0.5, want: max},
		{v: -1, signed: true, idle: -0.5, want: 0},
		{v: 0, signed: false, idle: -0.5, want: 0}, // unsigned ignores it
	}
	for _, c := range cases {
		if got := throttleToRaw(c.v, c.signed, c.idle, max); got != c.want {
			t.Errorf("throttleToRaw(%v, signed=%v, idle=%v) = %d, want %d", c.v, c.signed, c.idle, got, c.want)
		}
	}
}
