// Package config loads and saves the server's JSON configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"mhotas/internal/calibration"
)

// FailsafeAction is what an axis does when AXIS packets time out.
type FailsafeAction string

const (
	FailsafeNeutral FailsafeAction = "neutral"
	FailsafeHold    FailsafeAction = "hold"
)

type AxisConfig struct {
	Curve    calibration.Curve `json:"curve"`
	Failsafe FailsafeAction    `json:"failsafe"`

	// Smoothing is the weight of the previous value (0 = off, toward 1 =
	// heavier). Applied by transport after Curve since it needs history.
	Smoothing float64 `json:"smoothing"`

	// Throttle only. SignedThrottle sends one continuous signed value instead
	// of SimConnect's arm-toggle plus positive magnitude. SignedThrottleIdle
	// is where idle falls on that -1..1 scale; it depends on the aircraft, so
	// it is a tuned number (0 = no remap).
	SignedThrottle     bool    `json:"signed_throttle,omitempty"`
	SignedThrottleIdle float64 `json:"signed_throttle_idle,omitempty"`
}

// ControlMapping is one Config.Controls entry. It is not validated here;
// control/simconnect's driver factory owns the meaning of Driver and fails
// at backend construction on an unknown one.
type ControlMapping struct {
	Driver string `json:"driver"`
	Event  string `json:"event,omitempty"` // kevent: the K:EVENT name to transmit
	Name   string `json:"name,omitempty"`  // input_event: the Input Event name to resolve
	Invert bool   `json:"invert,omitempty"`
}

// AircraftProfile swaps the variable a TELEMETRY field is read from, for an
// aircraft that keeps its real state outside the stock simvars: the FBW
// A32NX never sets BRAKE PARKING POSITION, so without one the phone showed
// its parking brake off while it was set.
type AircraftProfile struct {
	// TitleContains matches the sim's aircraft TITLE, case-insensitively.
	TitleContains string `json:"title_contains"`
	// Telemetry maps a TelemetryFields name to "L:VAR", "SIMVAR,unit", or a
	// number for a constant.
	Telemetry map[string]string `json:"telemetry"`
}

// TelemetryFields are the AircraftProfile.Telemetry keys, in TELEMETRY's
// units: flap index and max as integers, spoilers_handle 0..1, the rest
// nonzero = true.
var TelemetryFields = []string{"flaps_index", "flaps_max", "spoilers_handle", "spoilers_armed", "parking_brake"}

// Config is the full server configuration.
type Config struct {
	ListenPort        int        `json:"listen_port"`
	FailsafeTimeoutMS int        `json:"failsafe_timeout_ms"`
	Pitch             AxisConfig `json:"pitch"`
	Roll              AxisConfig `json:"roll"`
	Throttle          AxisConfig `json:"throttle"`
	Rudder            AxisConfig `json:"rudder"`
	// Speedbrake uses only Curve.Invert (retracted and full swap, like MSFS's
	// "reverse axis") and always holds on failsafe.
	Speedbrake AxisConfig `json:"speedbrake"`

	// Controls overrides per-control mappings by name; absent names use the
	// backend's defaults.
	Controls map[string]ControlMapping `json:"controls,omitempty"`

	// Aircraft is checked in order; the first match wins.
	Aircraft []AircraftProfile `json:"aircraft,omitempty"`
}

// Default holds throttle on failsafe: a sudden throttle cut is a worse
// surprise than a stale hold.
func Default() Config {
	return Config{
		ListenPort:        9750,
		FailsafeTimeoutMS: 500,
		Pitch:             AxisConfig{Failsafe: FailsafeNeutral},
		Roll:              AxisConfig{Failsafe: FailsafeNeutral},
		Throttle:          AxisConfig{Failsafe: FailsafeHold},
		Rudder:            AxisConfig{Failsafe: FailsafeNeutral},
		Speedbrake:        AxisConfig{Failsafe: FailsafeHold},
		Aircraft: []AircraftProfile{{
			// Its stock flap index trails the handle by seconds as the flaps
			// travel and skips 1, and its stock spoiler handle is a slewed
			// surface position capped at 40% on the ground.
			TitleContains: "A32NX",
			Telemetry: map[string]string{
				"flaps_index":     "L:A32NX_FLAPS_HANDLE_INDEX",
				"flaps_max":       "4",
				"spoilers_handle": "L:A32NX_SPOILERS_HANDLE_POSITION",
				"spoilers_armed":  "L:A32NX_SPOILERS_ARMED",
				"parking_brake":   "L:A32NX_PARK_BRAKE_LEVER_POS",
			},
		}},
	}
}

// Load overlays the JSON file at path onto Default(). An empty or missing
// path yields Default().
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("config: reading %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes cfg to path; an empty path is a no-op.
func Save(path string, cfg Config) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshaling: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("config: writing %s: %w", path, err)
	}
	return nil
}

// validate rejects unknown failsafe values: transport would treat a typo
// like "netural" as hold, leaving stale pitch/roll/rudder applied on timeout.
func (c Config) validate() error {
	axes := map[string]AxisConfig{"pitch": c.Pitch, "roll": c.Roll, "throttle": c.Throttle, "rudder": c.Rudder, "speedbrake": c.Speedbrake}
	for name, ac := range axes {
		if ac.Failsafe != FailsafeNeutral && ac.Failsafe != FailsafeHold {
			return fmt.Errorf("%s.failsafe: %q is neither %q nor %q", name, ac.Failsafe, FailsafeNeutral, FailsafeHold)
		}
	}
	for i, p := range c.Aircraft {
		if p.TitleContains == "" {
			return fmt.Errorf("aircraft[%d].title_contains is empty, which would match every aircraft", i)
		}
		for field := range p.Telemetry {
			if !slices.Contains(TelemetryFields, field) {
				return fmt.Errorf("aircraft[%d].telemetry: unknown field %q (want one of %v)", i, field, TelemetryFields)
			}
		}
	}
	return nil
}
