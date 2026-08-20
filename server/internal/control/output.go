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
// The mapping from protocol.Button.ButtonID to a semantic ButtonID lives in
// server config, not here.
type ButtonID uint8

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
