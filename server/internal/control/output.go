// Package control defines the Output seam between transport and the simulator.
package control

import "fmt"

// AxisID identifies a continuous control axis.
type AxisID uint8

const (
	AxisPitch AxisID = iota
	AxisRoll
	AxisThrottle
	AxisRudder
	AxisSpeedbrake // 0..1, not -1..1
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
	case AxisSpeedbrake:
		return "speedbrake"
	default:
		return fmt.Sprintf("axis(%d)", uint8(a))
	}
}

// ButtonID values are wire-level: the app's ButtonIds class in
// control_screen.dart hardcodes the same numbers. Do not renumber.
type ButtonID uint8

const (
	ButtonGear ButtonID = iota + 1
	ButtonParkingBrake
	ButtonPause
	ButtonFlapIncr
	ButtonFlapDecr
	ButtonToeBrake
	_ // 7, 8: spoiler INC/DEC until the speedbrake became an axis
	_
	ButtonSpoilerArm
	ButtonAutopilotDisconnect
	ButtonTrimUp
	ButtonTrimDown
	ButtonThrottleIncr
	ButtonThrottleDecr
	ButtonThrottleReverseArm
)

var axisNames = map[string]AxisID{
	"pitch":      AxisPitch,
	"roll":       AxisRoll,
	"throttle":   AxisThrottle,
	"rudder":     AxisRudder,
	"speedbrake": AxisSpeedbrake,
}

var buttonNames = map[string]ButtonID{
	"gear":                 ButtonGear,
	"parking_brake":        ButtonParkingBrake,
	"pause":                ButtonPause,
	"flap_incr":            ButtonFlapIncr,
	"flap_decr":            ButtonFlapDecr,
	"toe_brake":            ButtonToeBrake,
	"spoiler_arm":          ButtonSpoilerArm,
	"autopilot_disconnect": ButtonAutopilotDisconnect,
	"trim_up":              ButtonTrimUp,
	"trim_down":            ButtonTrimDown,
	"throttle_incr":        ButtonThrottleIncr,
	"throttle_decr":        ButtonThrottleDecr,
	"throttle_reverse_arm": ButtonThrottleReverseArm,
}

func (b ButtonID) String() string {
	for name, id := range buttonNames {
		if id == b {
			return name
		}
	}
	return fmt.Sprintf("button(%d)", uint8(b))
}

// AxisByName resolves a config "controls" key.
func AxisByName(name string) (AxisID, bool) {
	id, ok := axisNames[name]
	return id, ok
}

// ButtonByName resolves a config "controls" key.
func ButtonByName(name string) (ButtonID, bool) {
	id, ok := buttonNames[name]
	return id, ok
}

// Output is a control-injection backend. Values are already shaped by
// calibration; a backend only converts range and writes.
type Output interface {
	Name() string

	// SetAxis takes -1..1 (negative throttle is reverse thrust).
	SetAxis(axis AxisID, value float64) error

	SetButton(id ButtonID, pressed bool) error

	// Ready is for status display only; SetAxis/SetButton already tolerate a
	// not-ready backend.
	Ready() bool

	Close() error
}

// ThrottleSyncer is for backends whose throttle axis holds its last value
// (vJoy). value is in SetAxis units (post-curve).
type ThrottleSyncer interface {
	SyncThrottle(value float64, autothrottle bool)
}

// SignedThrottleSetter is optionally implemented by backends with throttle
// settings that change live; transport type-asserts for it.
//
// SetThrottleInvert reverses the final output after the idle remap, like
// MSFS's "reverse axis" (vJoy only). Inverting before the remap, as
// calibration.Curve does for other axes, swaps forward and reverse around
// idle.
type SignedThrottleSetter interface {
	SetSignedThrottle(signed bool)
	SetSignedThrottleIdle(idle float64)
	SetThrottleInvert(invert bool)
}
