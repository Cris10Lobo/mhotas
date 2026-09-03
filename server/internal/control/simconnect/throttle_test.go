package simconnect

import "testing"

func TestToSignedThrottleRaw(t *testing.T) {
	cases := []struct {
		value, idlePoint float64
		want             int32
	}{
		{value: 0, idlePoint: 0, want: 0},            // no remap: identity
		{value: 1, idlePoint: 0, want: 16383},        // no remap: identity
		{value: -1, idlePoint: 0, want: -16383},      // no remap: identity
		{value: 0, idlePoint: -0.5, want: -8191},     // idle lands at idlePoint, not raw 0
		{value: 1, idlePoint: -0.5, want: 16383},     // full power still reaches the top
		{value: -1, idlePoint: -0.5, want: -16383},   // full reverse still reaches the bottom
		{value: 0.5, idlePoint: -0.5, want: 4095},    // halfway to full power from idle
		{value: -0.5, idlePoint: -0.5, want: -12287}, // halfway to full reverse from idle
		{value: 2, idlePoint: -0.5, want: 16383},     // clamps
		{value: -2, idlePoint: -0.5, want: -16383},   // clamps

		// stockIdlePoint (-1): the arm-toggle convention's fixed mapping,
		// what used to be a separate toThrottleRaw function — idle at the
		// negative extreme, midpoint NOT idle.
		{value: 0, idlePoint: stockIdlePoint, want: -16383}, // idle
		{value: 1, idlePoint: stockIdlePoint, want: 16383},  // full power
		{value: 0.5, idlePoint: stockIdlePoint, want: 0},    // midpoint, NOT idle
		{value: 2, idlePoint: stockIdlePoint, want: 16383},  // clamps
	}
	for _, c := range cases {
		if got := toSignedThrottleRaw(c.value, c.idlePoint, 16383); got != c.want {
			t.Errorf("toSignedThrottleRaw(%v, %v) = %v, want %v", c.value, c.idlePoint, got, c.want)
		}
	}
}
