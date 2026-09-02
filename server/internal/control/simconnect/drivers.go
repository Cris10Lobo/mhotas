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

// Driver kinds this backend's factory understands. A config.ControlMapping
// naming any other Driver value fails at Backend construction — see
// buildAxisDriver/buildButtonDriver.
const (
	driverKEvent      = "kevent"       // generic named K:EVENT, see simlink.Client.TransmitNamed
	driverInputEvent  = "input_event"  // MSFS2024 Input Event, see simlink.Client.SetInputEvent
	driverThrottleEx1 = "throttle_ex1" // idle-range throttle; reverse convention switchable live, see throttleEx1Driver
	driverToeBrake    = "toe_brake"    // momentary, needs both press and release
	driverPauseToggle = "pause_toggle" // explicit target computed from Client.Paused(), not a bare toggle
	driverSpoilerArm  = "spoiler_arm"  // explicit value + locally-tracked arm state, no telemetry readback
	driverReverseArm  = "reverse_arm"  // momentary; shares Client.SetReverseArmed's state with throttleEx1Driver
)

// defaultMappings is every control this backend drives today, expressed as
// the same declarative data a server.config.json "controls" override would
// use — moved out of imperative case branches, not preserved as a separate
// hardcoded path. An empty/absent config.Config.Controls reproduces this
// exactly.
func defaultMappings() map[string]config.ControlMapping {
	return map[string]config.ControlMapping{
		"pitch":    {Driver: driverKEvent, Event: "AXIS_ELEVATOR_SET", Invert: true},
		"roll":     {Driver: driverKEvent, Event: "AXIS_AILERONS_SET"},
		"rudder":   {Driver: driverKEvent, Event: "AXIS_RUDDER_SET"},
		"throttle": {Driver: driverThrottleEx1},

		"gear":                 {Driver: driverKEvent, Event: "GEAR_TOGGLE"},
		"parking_brake":        {Driver: driverKEvent, Event: "PARKING_BRAKES"},
		"pause":                {Driver: driverPauseToggle},
		"flap_incr":            {Driver: driverKEvent, Event: "FLAPS_INCR"},
		"flap_decr":            {Driver: driverKEvent, Event: "FLAPS_DECR"},
		"toe_brake":            {Driver: driverToeBrake},
		"spoiler_incr":         {Driver: driverKEvent, Event: "SPOILERS_INC"},
		"spoiler_decr":         {Driver: driverKEvent, Event: "SPOILERS_DEC"},
		"spoiler_arm":          {Driver: driverSpoilerArm},
		"autopilot_disconnect": {Driver: driverKEvent, Event: "AUTOPILOT_OFF"},
		"trim_up":              {Driver: driverKEvent, Event: "ELEV_TRIM_UP"},
		"trim_down":            {Driver: driverKEvent, Event: "ELEV_TRIM_DN"},

		// Nudge-throttle fallback (app's +/- buttons + reverse-arm hold):
		// relative to the sim's own current engine state via the same
		// standard SimConnect events F2/F3 use, not an absolute position on
		// any aircraft-specific calibrated scale — unlike the "throttle"
		// axis above, this is the same on every aircraft.
		"throttle_incr":        {Driver: driverKEvent, Event: "INCREASE_THROTTLE"},
		"throttle_decr":        {Driver: driverKEvent, Event: "DECREASE_THROTTLE"},
		"throttle_reverse_arm": {Driver: driverReverseArm},
	}
}

// buildMappedOutput resolves the effective control mapping (defaults
// overridden per-name by overrides) into a control.MappedOutput, via this
// backend's driver factory. Fails closed: an override naming an
// unrecognized control or driver kind is a config error, surfaced at
// startup rather than silently ignored.
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

// kEventAxisDriver fires a generic named K:EVENT with a linearly-scaled
// axis value — the shape of every axis this project drives except
// throttle (see throttleEx1Driver). invert flips the value before scaling,
// for events like AXIS_ELEVATOR_SET whose positive direction doesn't match
// mHOTAS's own "positive = climb" convention.
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

// kEventButtonDriver fires a generic named K:EVENT on press, ignoring
// release — the shape of every button this project drives except toe
// brake (momentary), pause/spoiler-arm (explicit-value, not fire-and-forget).
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
// conventions, switchable live (SetSignedThrottle/SetSignedThrottleIdle, via
// the phone's Settings screen and CONFIG_SET) rather than fixed at
// construction, since which one's correct — and where "idle" falls on the
// signed scale — depends on the aircraft currently loaded, not anything
// knowable when the backend was built:
//
//   - Default (signed=false): arm/disarm reverse via SetReverseArmed on a
//     sign transition, then send the same idle..full raw magnitude either
//     way — confirmed correct via an Asobo developer forum post
//     (devsupport.flightsimulator.com/t/throttle-set-event-ids-explanation/
//     4571). Once armed, MSFS applies that magnitude as negative (reverse)
//     power internally; this mode never sends a negative raw value itself.
//   - Signed (signed=true): send one continuous raw signed axis, no arm
//     event at all, via toSignedThrottleRaw (see its own doc comment) —
//     some aircraft's own throttle-calibration systems expect this instead
//     of the arm-toggle dance. idlePoint defaults to 0 (no remap, identity
//     mapping) and is a plain tunable, not a guessed constant — this
//     project has no way to know where a given aircraft's own calibration
//     puts "idle" on that scale.
type throttleEx1Driver struct {
	client *simlink.Client
	log    *slog.Logger

	signed atomic.Bool

	idleMu    sync.Mutex
	idlePoint float64
}

func (d *throttleEx1Driver) SetSignedThrottle(signed bool) { d.signed.Store(signed) }

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
	return d.client.SetThrottle(toThrottleRaw(magnitude, simlink.AxisRange))
}

// toeBrakeDriver is momentary (apply while held, release on lift), unlike
// the press-only kevent buttons — it needs the release edge.
type toeBrakeDriver struct {
	client *simlink.Client
}

func (d *toeBrakeDriver) SetButton(pressed bool) error {
	return d.client.SetToeBrakes(pressed)
}

// reverseArmDriver is the nudge-throttle fallback's hold-to-arm reverse
// button — momentary, like toeBrakeDriver, but through
// Client.SetReverseArmed rather than a bare toggle, so it can't desync from
// throttleEx1Driver's own arm calls on the lever (see Client.SetReverseArmed).
type reverseArmDriver struct {
	client *simlink.Client
}

func (d *reverseArmDriver) SetButton(pressed bool) error {
	return d.client.SetReverseArmed(pressed)
}

// pauseToggleDriver fires on press only, flipping whatever SimConnect's own
// Pause system event last reported (Client.Paused()) rather than a locally-
// tracked guess — PAUSE_SET needs an explicit target, not a toggle.
type pauseToggleDriver struct {
	client *simlink.Client
}

func (d *pauseToggleDriver) SetButton(pressed bool) error {
	if !pressed {
		return nil
	}
	return d.client.SetPause(!d.client.Paused())
}

// spoilerArmDriver fires on press only, toggling its own locally-tracked
// arm state — SPOILERS_ARM_SET takes an explicit value, not a toggle, but
// unlike pause there's no telemetry readback to flip against yet (see
// simlink.Client.SetSpoilersArmed).
//
// ponytail: this can drift from the app's own independent copy of the same
// bool (control_screen.dart's _speedbrakeArmed) — a server restart, an app
// restart, or a dropped 3x-redundant BUTTON packet leaves one side's guess
// wrong with nothing to reconcile it. Add spoilersArmed to TELEMETRY
// (matching parkingBrakeSet/paused's readback pattern) if this bites in
// practice.
type spoilerArmDriver struct {
	client *simlink.Client

	mu    sync.Mutex
	armed bool
}

func (d *spoilerArmDriver) SetButton(pressed bool) error {
	if !pressed {
		return nil
	}
	d.mu.Lock()
	d.armed = !d.armed
	armed := d.armed
	d.mu.Unlock()
	return d.client.SetSpoilersArmed(armed)
}

// inputEventAxisDriver writes a DOUBLE-typed MSFS2024 Input Event by name,
// resolving it against the current session's enumeration on every call
// (never cached across reconnects/aircraft changes — see
// simlink.Client.SetInputEvent). Fire-and-forget, same as the kevent path:
// no confirmation the write actually landed.
type inputEventAxisDriver struct {
	client *simlink.Client
	name   string
}

func (d *inputEventAxisDriver) SetAxis(value float64) error {
	return d.client.SetInputEvent(d.name, value)
}

// inputEventButtonDriver is inputEventAxisDriver's button-shaped
// counterpart: fires on press only, writing 1.0/0.0.
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
