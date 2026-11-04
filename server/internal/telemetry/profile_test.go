package telemetry

import (
	"testing"

	"mhotas/internal/config"
	"mhotas/pkg/protocol"
)

func TestA32NXProfileOverridesStockState(t *testing.T) {
	p := matchProfile(config.Default().Aircraft, "Airbus A320 Neo (A32NX) AirAsia India")
	if p == nil {
		t.Fatal("default A32NX profile didn't match")
	}
	o := newOverrides(p)
	if len(o.names) != 7 || o.consts["flaps_max"] != 4 {
		t.Fatalf("overrides = %+v, want 7 vars and flaps_max const 4", o)
	}

	// Stock simvars as read live with the A32NX parked, flaps 1, spoilers armed.
	tel := protocol.Telemetry{FlapHandleIndex: 2, FlapHandleIndexMax: 5, SpoilersArmed: true, SpoilersHandle: 0.4, ThrottlePercent: 0.8}
	values := map[string]float64{"autopilot": 1, "autothrottle_status": 2, "throttle_quadrant": 25, "flaps_index": 1, "spoilers_handle": 1, "spoilers_armed": 0, "parking_brake": 1}
	read := make([]float64, len(o.fields))
	for i, f := range o.fields {
		read[i] = values[f]
	}
	o.apply(&tel, read)
	want := protocol.Telemetry{FlapHandleIndex: 1, FlapHandleIndexMax: 4, SpoilersHandle: 1, SpoilersArmed: false, ParkingBrakeSet: true, AutopilotMaster: true, AutothrottleActive: true}
	if tel != want {
		t.Fatalf("applied = %+v, want %+v", tel, want)
	}

	// No reading yet: constants still apply, read fields stay stock.
	tel = protocol.Telemetry{FlapHandleIndex: 2, FlapHandleIndexMax: 5}
	o.apply(&tel, nil)
	if tel.FlapHandleIndex != 2 || tel.FlapHandleIndexMax != 4 {
		t.Fatalf("before first reading = %+v, want stock index 2, max 4", tel)
	}
}

func TestThrottleQuadrantIgnoresAutothrustThrust(t *testing.T) {
	for _, c := range []struct {
		name string
		v    float64
		want float32
	}{
		{"full reverse", 0, -1},
		{"idle", 25, 0},
		{"CLB", 50, 1.0 / 3},
		{"TOGA", 100, 1},
	} {
		tel := protocol.Telemetry{ThrottlePercent: 0.8} // stock reading under autothrust
		setField(&tel, "throttle_quadrant", c.v)
		if tel.ThrottlePercent != c.want {
			t.Errorf("%s: got %v, want %v", c.name, tel.ThrottlePercent, c.want)
		}
	}
}

func TestAutothrottleStatusArmedIsNotActive(t *testing.T) {
	for v, want := range map[float64]bool{0: false, 1: false, 2: true} {
		var tel protocol.Telemetry
		setField(&tel, "autothrottle_status", v)
		if tel.AutothrottleActive != want {
			t.Errorf("status %v: active = %v, want %v", v, tel.AutothrottleActive, want)
		}
	}
}

func TestOverridesNotReadyUntilFullReading(t *testing.T) {
	o := newOverrides(matchProfile(config.Default().Aircraft, "A32NX"))
	if o.ready(nil) {
		t.Fatal("ready before the first reading, so a stock throttle would reach vJoy")
	}
	if !o.ready(make([]float64, len(o.fields))) {
		t.Fatal("not ready with a full reading")
	}
	if !newOverrides(nil).ready(nil) {
		t.Fatal("an aircraft with no profile has nothing to wait for")
	}
}

func TestNoProfileForStockAircraft(t *testing.T) {
	if p := matchProfile(config.Default().Aircraft, "Cessna Skyhawk G1000"); p != nil {
		t.Fatalf("matched %+v for a stock aircraft", p)
	}
	o := newOverrides(nil)
	tel := protocol.Telemetry{FlapHandleIndex: 2}
	o.apply(&tel, nil)
	if tel.FlapHandleIndex != 2 {
		t.Fatal("no profile changed telemetry")
	}
}
