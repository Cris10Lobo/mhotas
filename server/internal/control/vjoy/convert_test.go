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
		if got := throttleToRaw(c.v, c.signed, c.idle, false, max); got != c.want {
			t.Errorf("throttleToRaw(%v, signed=%v, idle=%v) = %d, want %d", c.v, c.signed, c.idle, got, c.want)
		}
	}
}

// Invert must mirror the final axis position (like MSFS's "reverse axis"),
// not the input: with idle at -0.45, inverting the input put phone idle at
// 73% of the axis under a reversed MSFS binding — read in the sim as ~75%.
func TestThrottleInvertMirrorsAxis(t *testing.T) {
	const max = 32767
	for _, v := range []float64{0, 0.5, 1, -0.5, -1} {
		plain := throttleToRaw(v, true, -0.45, false, max)
		inverted := throttleToRaw(v, true, -0.45, true, max)
		if plain+inverted != max {
			t.Errorf("v=%v: plain %d + inverted %d = %d, want %d (mirror)", v, plain, inverted, plain+inverted, max)
		}
	}
}
