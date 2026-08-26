// Package config loads server configuration: listen port, per-axis
// calibration curves, and failsafe policy. Backed by a hand-edited JSON
// file; per-axis dead-zone/expo/invert/smoothing can also be edited live
// from the phone (CONFIG_SET, see server/internal/transport), which calls
// Save to persist the change back to the same file.
package config

import (
	"encoding/json"
	"fmt"
	"os"

	"mhotas/internal/calibration"
)

// FailsafeAction describes what a given axis should do when the server
// stops receiving AXIS packets past the failsafe timeout.
type FailsafeAction string

const (
	FailsafeNeutral FailsafeAction = "neutral"
	FailsafeHold    FailsafeAction = "hold"
)

// AxisConfig groups a calibration curve with the failsafe policy for one axis.
type AxisConfig struct {
	Curve    calibration.Curve `json:"curve"`
	Failsafe FailsafeAction    `json:"failsafe"`

	// Smoothing blends each new shaped value with the previous one (0 =
	// off/instant, closer to 1 = slower/heavier smoothing) so a fast, jerky
	// phone tilt doesn't reach the sim as a raw spike. Applied in
	// transport.Server, after Curve — it's temporal (needs the previous
	// sample) rather than a pure per-value shaping function like Curve.
	Smoothing float64 `json:"smoothing"`
}

// ControlMapping declares how one named control (a key in Config.Controls,
// e.g. "throttle", "gear" — resolved against control.AxisByName/ButtonByName)
// actually reaches the simulator. Driver is backend-specific data, not
// validated here: control/simconnect's driver factory is the authority on
// which Driver kinds ("kevent", "input_event", ...) and fields it
// understands — this struct only carries the JSON through. A backend that
// doesn't recognize a Driver kind, or finds an entry naming a control it
// doesn't drive, fails at backend construction, not here.
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

	// Controls overrides how individual controls reach the simulator, keyed
	// by name ("throttle", "gear", "autopilot_disconnect", ...). A name with
	// no entry here uses the active backend's own default mapping — an
	// empty/absent Controls section reproduces that backend's built-in
	// behavior exactly.
	Controls map[string]ControlMapping `json:"controls,omitempty"`
}

// Default returns the MVP default configuration: port 9750, 500ms failsafe
// timeout, no dead-zone/expo shaping. Pitch/roll/rudder snap to neutral and
// throttle holds its last value on failsafe — an unexpected throttle cut
// is a worse mid-flight surprise than a stale hold.
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

// Load reads a JSON config file at path, starting from Default() and
// overriding whatever fields the file specifies. If path is empty or the
// file doesn't exist, Default() is returned unchanged — a config file is
// optional for MVP.
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

// Save writes cfg as JSON to path. Empty path is a no-op, mirroring Load's
// "no config file" case — used so a live phone-side calibration edit
// (server/internal/transport's CONFIG_SET handling) persists across a
// restart when the server was launched with -config, and is silently
// skipped rather than erroring when it wasn't.
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

// validate rejects a failsafe value that isn't exactly "neutral" or
// "hold". Without this, applyFailsafe (transport/server.go) treats any
// other value — a typo like "netural" — as "hold" silently, which for
// pitch/roll/rudder means an unattended AXIS timeout leaves stale input
// applied indefinitely instead of the safety-critical snap to neutral.
func (c Config) validate() error {
	axes := map[string]AxisConfig{"pitch": c.Pitch, "roll": c.Roll, "throttle": c.Throttle, "rudder": c.Rudder}
	for name, ac := range axes {
		if ac.Failsafe != FailsafeNeutral && ac.Failsafe != FailsafeHold {
			return fmt.Errorf("%s.failsafe: %q is neither %q nor %q", name, ac.Failsafe, FailsafeNeutral, FailsafeHold)
		}
	}
	return nil
}
