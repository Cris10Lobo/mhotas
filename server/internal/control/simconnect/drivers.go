//go:build windows

package simconnect

import (
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"sync/atomic"

	"mhotas/internal/config"
	"mhotas/internal/control"
	"mhotas/internal/simlink"
)

const (
	driverKEvent      = "kevent"
	driverInputEvent  = "input_event"
	driverThrottleEx1 = "throttle_ex1"
	driverToeBrake    = "toe_brake"
	driverPauseToggle = "pause_toggle"
	driverSpoilerArm  = "spoiler_arm"
	driverReverseArm  = "reverse_arm"
)

// defaultMappings is the per-control driver table that config "controls" overrides by name.
func defaultMappings() map[string]config.ControlMapping {
	return map[string]config.ControlMapping{
		"pitch":    {Driver: driverKEvent, Event: "AXIS_ELEVATOR_SET", Invert: true},
		"roll":     {Driver: driverKEvent, Event: "AXIS_AILERONS_SET"},
		"rudder":   {Driver: driverKEvent, Event: "AXIS_RUDDER_SET"},
		"throttle": {Driver: driverThrottleEx1},
		// An absolute position, unlike SPOILERS_INC/DEC, which some aircraft
		// (FBW A32NX) ignore.
		"speedbrake": {Driver: driverKEvent, Event: "SPOILERS_SET"},

		"gear":                 {Driver: driverKEvent, Event: "GEAR_TOGGLE"},
		"parking_brake":        {Driver: driverKEvent, Event: "PARKING_BRAKES"},
		"pause":                {Driver: driverPauseToggle},
		"flap_incr":            {Driver: driverKEvent, Event: "FLAPS_INCR"},
		"flap_decr":            {Driver: driverKEvent, Event: "FLAPS_DECR"},
		"toe_brake":            {Driver: driverToeBrake},
		"spoiler_arm":          {Driver: driverSpoilerArm},
		"autopilot_disconnect": {Driver: driverKEvent, Event: "AUTOPILOT_OFF"},
		"trim_up":              {Driver: driverKEvent, Event: "ELEV_TRIM_UP"},
		"trim_down":            {Driver: driverKEvent, Event: "ELEV_TRIM_DN"},

		// Nudge throttle is relative to the engine's current state, so unlike
		// the "throttle" axis it behaves the same on every aircraft.
		"throttle_incr":        {Driver: driverKEvent, Event: "INCREASE_THROTTLE"},
		"throttle_decr":        {Driver: driverKEvent, Event: "DECREASE_THROTTLE"},
		"throttle_reverse_arm": {Driver: driverReverseArm},
	}
}

// buildMappedOutput applies overrides on top of defaultMappings. An unknown
// control or driver name is an error rather than silently ignored.
func buildMappedOutput(client *simlink.Client, log *slog.Logger, overrides map[string]config.ControlMapping) (*control.MappedOutput, error) {
	effective := defaultMappings()
	maps.Copy(effective, overrides)

	axes := make(map[control.AxisID]control.AxisDriver)
	buttons := make(map[control.ButtonID]control.ButtonDriver)
	for name, m := range effective {
		if axisID, ok := control.AxisByName(name); ok {
			d, err := buildAxisDriver(client, log, m)
			if err != nil {
				return nil, fmt.Errorf("control %q: %w", name, err)
			}
			axes[axisID] = d
			continue
		}
		if buttonID, ok := control.ButtonByName(name); ok {
			d, err := buildButtonDriver(client, m)
			if err != nil {
				return nil, fmt.Errorf("control %q: %w", name, err)
			}
			buttons[buttonID] = d
			continue
		}
		return nil, fmt.Errorf("control %q: not a recognized axis or button name", name)
	}

	return control.NewMappedOutput("simconnect", axes, buttons, client.Ready, client.Close), nil
}

func buildAxisDriver(client *simlink.Client, log *slog.Logger, m config.ControlMapping) (control.AxisDriver, error) {
	switch m.Driver {
	case driverKEvent:
		if m.Event == "" {
			return nil, fmt.Errorf("kevent driver needs \"event\"")
		}
		client.RegisterEvent(m.Event)
		return &kEventAxisDriver{client: client, event: m.Event, invert: m.Invert}, nil
	case driverInputEvent:
		if m.Name == "" {
			return nil, fmt.Errorf("input_event driver needs \"name\"")
		}
		return &inputEventAxisDriver{client: client, name: m.Name}, nil
	case driverThrottleEx1:
		return &throttleEx1Driver{client: client, log: log}, nil
	default:
		return nil, fmt.Errorf("unknown axis driver %q", m.Driver)
	}
}

func buildButtonDriver(client *simlink.Client, m config.ControlMapping) (control.ButtonDriver, error) {
	switch m.Driver {
	case driverKEvent:
		if m.Event == "" {
			return nil, fmt.Errorf("kevent driver needs \"event\"")
		}
		client.RegisterEvent(m.Event)
		return &kEventButtonDriver{client: client, event: m.Event}, nil
	case driverInputEvent:
		if m.Name == "" {
			return nil, fmt.Errorf("input_event driver needs \"name\"")
		}
		return &inputEventButtonDriver{client: client, name: m.Name}, nil
	case driverToeBrake:
		return &toeBrakeDriver{client: client}, nil
	case driverPauseToggle:
		return &pauseToggleDriver{client: client}, nil
	case driverSpoilerArm:
		return &spoilerArmDriver{client: client}, nil
	case driverReverseArm:
		return &reverseArmDriver{client: client}, nil
	default:
		return nil, fmt.Errorf("unknown button driver %q", m.Driver)
	}
}

// kEventAxisDriver fires a named K:EVENT with the value scaled linearly.
// invert exists because AXIS_ELEVATOR_SET's positive direction is opposite
// mHOTAS's "positive = climb".
type kEventAxisDriver struct {
	client *simlink.Client
	event  string
	invert bool
}

func (d *kEventAxisDriver) SetAxis(value float64) error {
	if d.invert {
		value = -value
	}
	return d.client.TransmitNamed(d.event, uint32(toAxisRange(value)))
}

// kEventButtonDriver fires a named K:EVENT on press and ignores release.
type kEventButtonDriver struct {
	client *simlink.Client
	event  string
}

func (d *kEventButtonDriver) SetButton(pressed bool) error {
	if !pressed {
		return nil
	}
	return d.client.TransmitNamed(d.event, 0)
}

// throttleEx1Driver drives THROTTLE_AXIS_SET_EX1 under one of two
// conventions, switchable live because the right one depends on the loaded
// aircraft:
//
//   - Default: arm/disarm reverse via SetReverseArmed on a sign transition,
//     then send the same idle..full magnitude either way; MSFS applies it as
//     reverse power once armed.
//   - Signed: one continuous signed axis with no arm event, idle remapped to
//     idlePoint (a per-aircraft tunable, 0 = identity).
type throttleEx1Driver struct {
	client *simlink.Client
	log    *slog.Logger

	signed atomic.Bool

	idleMu    sync.Mutex
	idlePoint float64
}

func (d *throttleEx1Driver) SetSignedThrottle(signed bool) { d.signed.Store(signed) }

// SetThrottleInvert is a no-op: invert undoes a reversed MSFS axis binding,
// which SimConnect events bypass. Honoring a vJoy-tuned value here turned
// idle into up to full power.
func (d *throttleEx1Driver) SetThrottleInvert(bool) {}

func (d *throttleEx1Driver) SetSignedThrottleIdle(idle float64) {
	d.idleMu.Lock()
	d.idlePoint = idle
	d.idleMu.Unlock()
}

func (d *throttleEx1Driver) SetAxis(value float64) error {
	if d.signed.Load() {
		d.idleMu.Lock()
		idle := d.idlePoint
		d.idleMu.Unlock()
		return d.client.SetThrottle(toSignedThrottleRaw(value, idle, simlink.AxisRange))
	}

	wantReverse := value < 0
	if err := d.client.SetReverseArmed(wantReverse); err != nil {
		d.log.Warn("reverse thrust arm failed", "err", err, "want", wantReverse)
	}
	magnitude := value
	if wantReverse {
		magnitude = -value
	}
	return d.client.SetThrottle(toSignedThrottleRaw(magnitude, stockIdlePoint, simlink.AxisRange))
}

// toeBrakeDriver applies while held and releases on lift.
type toeBrakeDriver struct {
	client *simlink.Client
}

func (d *toeBrakeDriver) SetButton(pressed bool) error {
	return d.client.SetToeBrakes(pressed)
}

// reverseArmDriver arms reverse while held, via the shared SetReverseArmed state.
type reverseArmDriver struct {
	client *simlink.Client
}

func (d *reverseArmDriver) SetButton(pressed bool) error {
	return d.client.SetReverseArmed(pressed)
}

// pauseToggleDriver flips the sim's reported pause state on press.
type pauseToggleDriver struct {
	client *simlink.Client
}

func (d *pauseToggleDriver) SetButton(pressed bool) error {
	if !pressed {
		return nil
	}
	return d.client.SetPause(!d.client.Paused())
}

// spoilerArmDriver flips the sim's reported SPOILERS ARMED state on press;
// before any telemetry arrives it arms.
type spoilerArmDriver struct {
	client *simlink.Client
}

func (d *spoilerArmDriver) SetButton(pressed bool) error {
	if !pressed {
		return nil
	}
	state, _ := d.client.LatestState()
	return d.client.SetSpoilersArmed(state.SpoilersArmed == 0)
}

// inputEventAxisDriver writes a DOUBLE Input Event by name, resolved per call
// because hashes don't survive reconnects.
type inputEventAxisDriver struct {
	client *simlink.Client
	name   string
}

func (d *inputEventAxisDriver) SetAxis(value float64) error {
	return d.client.SetInputEvent(d.name, value)
}

// inputEventButtonDriver writes 1.0 on press, ignoring release.
type inputEventButtonDriver struct {
	client *simlink.Client
	name   string
}

func (d *inputEventButtonDriver) SetButton(pressed bool) error {
	if !pressed {
		return nil
	}
	return d.client.SetInputEvent(d.name, 1.0)
}
