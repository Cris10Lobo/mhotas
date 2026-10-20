// Package control defines the abstraction that lets the SimConnect backend
// (MVP) and a future vJoy backend coexist without the rest of the server
// caring which one is active.
package control

import "fmt"

// AxisID identifies a continuous control axis.
type AxisID uint8

const (
	AxisPitch AxisID = iota
	AxisRoll
	AxisThrottle
	AxisRudder
)

func (a AxisID) String() string {
	switch a {
	case AxisPitch:
		return "pitch"
	case AxisRoll:
		return "roll"
	case AxisThrottle:
		return "throttle"
	case AxisRudder:
		return "rudder"
	default:
		return fmt.Sprintf("axis(%d)", uint8(a))
	}
}

// ButtonID identifies a discrete control input, e.g. gear or parking brake.
// Values are wire-level: app/lib/ui/control_screen.dart's ButtonIds class
// hardcodes these same numbers independently (no shared codegen, see
// docs/protocol.md) — do not renumber without updating both sides.
type ButtonID uint8

const (
	ButtonGear ButtonID = iota + 1
	ButtonParkingBrake
	ButtonPause
	ButtonFlapIncr
	ButtonFlapDecr
	ButtonToeBrake
	ButtonSpoilerIncr
	ButtonSpoilerDecr
	ButtonSpoilerArm
	ButtonAutopilotDisconnect
	ButtonTrimUp
	ButtonTrimDown
	ButtonThrottleIncr
	ButtonThrottleDecr
	ButtonThrottleReverseArm
)

// axisNames/buttonNames back AxisByName/ButtonByName — the name a
// server.config.json "controls" entry is keyed by. Kept here, not in any one
// backend package, since the names are backend-agnostic: a vjoy mapping
// entry addresses "throttle" the same way a simconnect one does.
var axisNames = map[string]AxisID{
	"pitch":    AxisPitch,
	"roll":     AxisRoll,
	"throttle": AxisThrottle,
	"rudder":   AxisRudder,
}

var buttonNames = map[string]ButtonID{
	"gear":                 ButtonGear,
	"parking_brake":        ButtonParkingBrake,
	"pause":                ButtonPause,
	"flap_incr":            ButtonFlapIncr,
	"flap_decr":            ButtonFlapDecr,
	"toe_brake":            ButtonToeBrake,
	"spoiler_incr":         ButtonSpoilerIncr,
	"spoiler_decr":         ButtonSpoilerDecr,
	"spoiler_arm":          ButtonSpoilerArm,
	"autopilot_disconnect": ButtonAutopilotDisconnect,
	"trim_up":              ButtonTrimUp,
	"trim_down":            ButtonTrimDown,
	"throttle_incr":        ButtonThrottleIncr,
	"throttle_decr":        ButtonThrottleDecr,
	"throttle_reverse_arm": ButtonThrottleReverseArm,
}

// String returns the button's config name, e.g. "gear".
func (b ButtonID) String() string {
	for name, id := range buttonNames {
		if id == b {
			return name
		}
	}
	return fmt.Sprintf("button(%d)", uint8(b))
}

// AxisByName resolves a config "controls" key to an AxisID, false if it
// isn't a recognized axis name.
func AxisByName(name string) (AxisID, bool) {
	id, ok := axisNames[name]
	return id, ok
}

// ButtonByName resolves a config "controls" key to a ButtonID, false if it
// isn't a recognized button name.
func ButtonByName(name string) (ButtonID, bool) {
	id, ok := buttonNames[name]
	return id, ok
}

// Output is implemented by each control-injection backend. Values arriving
// here are already dead-zone/curve/invert-shaped by the calibration
// package; a backend's only remaining job is the final range conversion
// into whatever it expects (SimConnect's own ranges vs vJoy's 0-32767) and
// the actual write.
type Output interface {
	// Name identifies the backend for logging, e.g. "simconnect", "vjoy", "log".
	Name() string

	// SetAxis applies a shaped axis value, -1..1 (throttle's negative range
	// is reverse thrust).
	SetAxis(axis AxisID, value float64) error

	// SetButton applies a discrete button state change.
	SetButton(id ButtonID, pressed bool) error

	// Ready reports whether the backend is actually able to drive the
	// simulator right now (e.g. SimConnect has completed its handshake with
	// a running MSFS2024) — distinct from the process being up. SetAxis/
	// SetButton already tolerate a not-ready backend (non-fatal error,
	// logged and skipped); this exists purely for status display (the TUI)
	// so an operator can tell "server up, sim not connected yet" apart from
	// "everything's fine" without reading log scrollback.
	Ready() bool

	// Close releases any backend resources (SimConnect connection, vJoy
	// device handle, etc.).
	Close() error
}

// ThrottleSyncer is implemented by a backend whose throttle input holds its
// last written value (vJoy), so it can be moved to match the sim's actual
// lever after the sim changed it some other way. Fed by telemetry through
// transport.Server, which applies the throttle calibration first — value
// is in the same units SetAxis receives.
type ThrottleSyncer interface {
	SyncThrottle(value float64, autothrottle bool)
}

// SignedThrottleSetter is implemented by a backend whose throttle driver can
// switch, live, between the two reverse-thrust conventions SimConnect's
// THROTTLE_AXIS_SET_EX1 actually has: the default arm-toggle-then-positive-
// magnitude dance, or one continuous signed value sent straight through
// (SetSignedThrottle), with SetSignedThrottleIdle tuning where "idle" falls
// on that signed scale — a plain number, not a guessed aircraft-specific
// constant baked into this project; whichever aircraft is loaded is the
// only thing that actually knows. Optional — transport type-asserts for it
// rather than requiring it on Output, so a backend with nothing throttle-
// specific to switch (logbackend, vjoy) needs no stub.
type SignedThrottleSetter interface {
	SetSignedThrottle(signed bool)
	SetSignedThrottleIdle(idle float64)
}
