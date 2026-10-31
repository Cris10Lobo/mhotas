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
	if len(o.names) != 4 || o.consts["flaps_max"] != 4 {
		t.Fatalf("overrides = %+v, want 4 vars and flaps_max const 4", o)
	}

	// Stock simvars as read live with the A32NX parked, flaps 1, spoilers armed.
	tel := protocol.Telemetry{FlapHandleIndex: 2, FlapHandleIndexMax: 5, SpoilersArmed: true, SpoilersHandle: 0.4}
	values := map[string]float64{"flaps_index": 1, "spoilers_handle": 1, "spoilers_armed": 0, "parking_brake": 1}
	read := make([]float64, len(o.fields))
	for i, f := range o.fields {
		read[i] = values[f]
	}
	o.apply(&tel, read)
	want := protocol.Telemetry{FlapHandleIndex: 1, FlapHandleIndexMax: 4, SpoilersHandle: 1, SpoilersArmed: false, ParkingBrakeSet: true}
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
