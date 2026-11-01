package telemetry

import (
	"math"
	"strconv"
	"strings"

	"mhotas/internal/config"
	"mhotas/pkg/protocol"
)

func matchProfile(profiles []config.AircraftProfile, title string) *config.AircraftProfile {
	title = strings.ToLower(title)
	for i, p := range profiles {
		if strings.Contains(title, strings.ToLower(p.TitleContains)) {
			return &profiles[i]
		}
	}
	return nil
}

// overrides is a profile split into vars to read (fields[i] <- names[i]) and
// constants.
type overrides struct {
	fields, names, units []string
	consts               map[string]float64
}

func newOverrides(p *config.AircraftProfile) overrides {
	o := overrides{consts: map[string]float64{}}
	if p == nil {
		return o
	}
	for _, field := range config.TelemetryFields {
		spec, ok := p.Telemetry[field]
		if !ok {
			continue
		}
		if v, err := strconv.ParseFloat(spec, 64); err == nil {
			o.consts[field] = v
			continue
		}
		name, unit, ok := strings.Cut(spec, ",")
		if !ok {
			unit = "number"
		}
		o.fields, o.names, o.units = append(o.fields, field), append(o.names, name), append(o.units, unit)
	}
	return o
}

// apply sets the overridden fields; values (the read vars, in names order)
// is skipped until a reading of the right length has arrived.
func (o overrides) apply(t *protocol.Telemetry, values []float64) {
	if len(values) == len(o.fields) {
		for i, field := range o.fields {
			setField(t, field, values[i])
		}
	}
	for field, v := range o.consts {
		setField(t, field, v)
	}
}

func setField(t *protocol.Telemetry, field string, v float64) {
	switch field {
	case "flaps_index":
		t.FlapHandleIndex = int8(math.Round(v))
	case "flaps_max":
		t.FlapHandleIndexMax = int8(math.Round(v))
	case "spoilers_handle":
		t.SpoilersHandle = clampUnit(float32(v))
	case "spoilers_armed":
		t.SpoilersArmed = v != 0
	case "parking_brake":
		t.ParkingBrakeSet = v != 0
	}
}
