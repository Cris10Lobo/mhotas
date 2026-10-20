package vjoy

import "testing"

func TestThrottleToRaw(t *testing.T) {
	const max = 32767
	cases := []struct {
		v      float64
		signed bool
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
	}
	for _, c := range cases {
		if got := throttleToRaw(c.v, c.signed, max); got != c.want {
			t.Errorf("throttleToRaw(%v, signed=%v) = %d, want %d", c.v, c.signed, got, c.want)
		}
	}
}
