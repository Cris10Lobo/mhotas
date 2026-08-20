//go:build windows

// Package simconnect implements control.Output by driving MSFS2024
// through SimConnect, via server/internal/simlink. Validated against a
// live MSFS2024 session.
package simconnect

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"mhotas/internal/control"
	"mhotas/internal/simlink"
)

// MVP button IDs, matching app/lib/ui/control_screen.dart's ButtonIds. A
// config-driven mapping (more buttons, user-remappable) is a later-phase
// addition; this fixed set covers the MVP button row.
const (
	buttonGear         control.ButtonID = 1
	buttonParkingBrake control.ButtonID = 2
	buttonPause        control.ButtonID = 3
	buttonFlapIncr     control.ButtonID = 4
	buttonFlapDecr     control.ButtonID = 5
	buttonToeBrake     control.ButtonID = 6
)

type Backend struct {
	client *simlink.Client
	log    *slog.Logger

	// reverseArmed tracks whether we've fired SET_REVERSE_THRUST_ON, so
	// syncThrottle only fires the arm/disarm event on a sign transition,
	// not every packet. Throttle's failsafe policy defaults to "hold" (see
	// transport.applyFailsafe), in which case SetAxis for throttle is only
	// ever called from transport's single packet-handling goroutine — but
	// that policy is operator-configurable, and if throttle's failsafe is
	// set to "neutral", the failsafe loop's own goroutine can call SetAxis
	// concurrently with it, so this needs its own lock rather than relying
	// on that assumption holding.
	reverseArmedMu sync.Mutex
	reverseArmed   bool
}

// New connects to MSFS2024 in the background (auto-reconnecting if the
// sim isn't running yet) and returns immediately with a usable Backend.
// SetAxis/SetButton calls made before MSFS2024 is reachable return a
// non-fatal error, which the transport layer already logs and continues
// past.
func New(ctx context.Context, log *slog.Logger) (*Backend, error) {
	return &Backend{client: simlink.Connect(ctx, log), log: log}, nil
}

func (b *Backend) Name() string { return "simconnect" }

// SimlinkClient exposes the underlying SimConnect connection so
// cmd/mhotas-server can share it with telemetry.Source — one connection
// serves both the write path (this Backend) and the read path (telemetry).
func (b *Backend) SimlinkClient() *simlink.Client { return b.client }

func (b *Backend) SetAxis(axis control.AxisID, value float64) error {
	switch axis {
	case control.AxisPitch:
		// AXIS_ELEVATOR_SET's positive value is nose-down — confirmed
		// against a live sim (matches the documented SimConnect/
		// aeronautical convention). mHOTAS's own convention is
		// positive pitch = climb, so the sign is flipped here rather than
		// in calibration.Curve.Invert, since this is a fixed fact about
		// SimConnect, not a user preference.
		return b.client.SetElevator(toAxisRange(-value))
	case control.AxisRoll:
		return b.client.SetAileron(toAxisRange(value))
	case control.AxisThrottle:
		return b.syncThrottle(value)
	case control.AxisRudder:
		return b.client.SetRudder(toAxisRange(value))
	default:
		return fmt.Errorf("simconnect: unknown axis %v", axis)
	}
}

func (b *Backend) SetButton(id control.ButtonID, pressed bool) error {
	// Toe brake is momentary (apply while held, release on lift), unlike
	// gear/parking brake/pause which are all press-only toggles — so it
	// needs the release edge and must run before the !pressed early return.
	if id == buttonToeBrake {
		return b.client.SetToeBrakes(pressed)
	}
	if !pressed {
		return nil // gear/parking brake/pause/flaps all act on press only
	}
	switch id {
	case buttonGear:
		return b.client.ToggleGear()
	case buttonParkingBrake:
		return b.client.ToggleParkingBrake()
	case buttonFlapIncr:
		return b.client.IncrFlaps()
	case buttonFlapDecr:
		return b.client.DecrFlaps()
	case buttonPause:
		// PAUSE_SET needs an explicit target, not a toggle (see
		// simlink.Client.SetPause) — flip whatever SimConnect's own Pause
		// event last reported, not a locally-tracked guess.
		return b.client.SetPause(!b.client.Paused())
	default:
		return fmt.Errorf("simconnect: unknown button %v", id)
	}
}

func (b *Backend) Close() error {
	return b.client.Close()
}

// syncThrottle drives forward and reverse thrust through a single
// consistent path — SetThrottle (THROTTLE_AXIS_SET_EX1) for the
// continuous value, ToggleReverseThrust to arm/disarm on a sign
// transition — confirmed correct via an Asobo developer forum post
// (devsupport.flightsimulator.com/t/throttle-set-event-ids-explanation/
// 4571). Once armed, MSFS applies the same idle..full raw magnitude as
// negative (reverse) power internally; we never send a negative raw value
// ourselves. Deliberately does NOT mix in the plain AXIS_THROTTLE_SET
// event or the separate SET_REVERSE_THRUST_ON/OFF flag — either one
// confirmed live to leave the engine stuck unable to return to forward
// thrust after a reverse pull, since they don't reconcile with EX1's own
// armed state.
func (b *Backend) syncThrottle(value float64) error {
	wantReverse := value < 0
	b.reverseArmedMu.Lock()
	if wantReverse != b.reverseArmed {
		if err := b.client.ToggleReverseThrust(); err != nil {
			b.log.Warn("reverse thrust arm failed", "err", err, "want", wantReverse)
		} else {
			b.log.Info("reverse thrust", "armed", wantReverse)
			b.reverseArmed = wantReverse
		}
	}
	b.reverseArmedMu.Unlock()
	magnitude := value
	if wantReverse {
		magnitude = -value
	}
	return b.client.SetThrottle(toThrottleRaw(magnitude, simlink.AxisRange))
}

// toAxisRange maps a calibrated -1..1 value to SimConnect's
// -simlink.AxisRange..simlink.AxisRange integer range. Correct for
// elevator/aileron/rudder: they're naturally centered controls, where 0
// already means neutral. Throttle uses toThrottleRaw instead — see
// throttle.go for why.
func toAxisRange(v float64) int32 {
	if v > 1 {
		v = 1
	}
	if v < -1 {
		v = -1
	}
	return int32(v * simlink.AxisRange)
}
