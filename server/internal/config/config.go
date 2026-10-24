// Package config loads and saves the server's JSON configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"

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

// Config is the full server configuration.
type Config struct {
	ListenPort        int        `json:"listen_port"`
	FailsafeTimeoutMS int        `json:"failsafe_timeout_ms"`
	Pitch             AxisConfig `json:"pitch"`
	Roll              AxisConfig `json:"roll"`
	Throttle          AxisConfig `json:"throttle"`
	Rudder            AxisConfig `json:"rudder"`

	// Controls overrides per-control mappings by name; absent names use the
	// backend's defaults.
	Controls map[string]ControlMapping `json:"controls,omitempty"`
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
	axes := map[string]AxisConfig{"pitch": c.Pitch, "roll": c.Roll, "throttle": c.Throttle, "rudder": c.Rudder}
	for name, ac := range axes {
		if ac.Failsafe != FailsafeNeutral && ac.Failsafe != FailsafeHold {
			return fmt.Errorf("%s.failsafe: %q is neither %q nor %q", name, ac.Failsafe, FailsafeNeutral, FailsafeHold)
		}
	}
	return nil
}
