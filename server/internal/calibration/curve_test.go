package calibration

import (
	"math"
	"testing"
)

func approxEqual(a, b float64) bool {
	const eps = 1e-9
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < eps
}

func TestCurveLinearNoShaping(t *testing.T) {
	c := Curve{}
	for _, v := range []float64{-1, -0.5, 0, 0.5, 1} {
		if got := c.Apply(v); !approxEqual(got, v) {
			t.Errorf("Apply(%v) = %v, want %v", v, got, v)
		}
	}
}

func TestCurveDeadZone(t *testing.T) {
	c := Curve{DeadZone: 0.1}
	if got := c.Apply(0.05); got != 0 {
		t.Errorf("Apply(0.05) = %v, want 0 (inside dead zone)", got)
	}
	if got := c.Apply(-0.05); got != 0 {
		t.Errorf("Apply(-0.05) = %v, want 0 (inside dead zone)", got)
	}
	if got := c.Apply(1.0); !approxEqual(got, 1.0) {
		t.Errorf("Apply(1.0) = %v, want 1.0 (full deflection still reachable)", got)
	}
	if got := c.Apply(-1.0); !approxEqual(got, -1.0) {
		t.Errorf("Apply(-1.0) = %v, want -1.0 (full deflection still reachable)", got)
	}
}

func TestCurveDeadZoneAtOrAboveOne(t *testing.T) {
	c := Curve{DeadZone: 1}
	if got := c.Apply(1.0); got != 0 {
		t.Errorf("Apply(1.0) with DeadZone=1 = %v, want 0", got)
	}
}

func TestCurveInvert(t *testing.T) {
	c := Curve{Invert: true}
	if got := c.Apply(0.5); !approxEqual(got, -0.5) {
		t.Errorf("Apply(0.5) inverted = %v, want -0.5", got)
	}
}

func TestCurveExpoPreservesEndpoints(t *testing.T) {
	c := Curve{Expo: 1.0}
	if got := c.Apply(1.0); !approxEqual(got, 1.0) {
		t.Errorf("Apply(1.0) with full expo = %v, want 1.0", got)
	}
	if got := c.Apply(0.0); !approxEqual(got, 0.0) {
		t.Errorf("Apply(0.0) with full expo = %v, want 0.0", got)
	}
	if got := c.Apply(0.5); got >= 0.5 {
		t.Errorf("Apply(0.5) with full expo = %v, want < 0.5", got)
	}
}

func TestInverseUndoesApply(t *testing.T) {
	curves := []Curve{{}, {DeadZone: 0.1}, {Expo: 0.5}, {DeadZone: 0.05, Expo: 0.8, Invert: true}, {Expo: 1}}
	for _, c := range curves {
		for _, v := range []float64{-1, -0.6, -0.25, 0, 0.1, 0.3125, 0.5, 0.9, 1} {
			if got := c.Apply(c.Inverse(v)); math.Abs(got-v) > 1e-6 {
				t.Errorf("%+v: Apply(Inverse(%v)) = %v", c, v, got)
			}
		}
	}
}
