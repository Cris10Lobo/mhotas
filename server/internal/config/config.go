// Package config loads server configuration: listen port, per-axis
// calibration curves, and failsafe policy. MVP config is a hand-edited JSON
// file; a phone-side remote settings UI is Phase 3, not MVP.
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

// Config is the full server configuration.
type Config struct {
	ListenPort        int        `json:"listen_port"`
	FailsafeTimeoutMS int        `json:"failsafe_timeout_ms"`
	Pitch             AxisConfig `json:"pitch"`
	Roll              AxisConfig `json:"roll"`
	Throttle          AxisConfig `json:"throttle"`
	Rudder            AxisConfig `json:"rudder"`
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
