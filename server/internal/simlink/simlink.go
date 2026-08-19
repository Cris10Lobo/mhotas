//go:build windows

// Package simlink isolates github.com/mrlm-net/simconnect's actual API
// from the rest of the server (server/internal/control/simconnect and
// server/internal/telemetry), so a binding swap doesn't ripple through the
// rest of the codebase. Connection pattern and event names below were
// validated against a live MSFS2024 session.
package simlink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/mrlm-net/simconnect/pkg/engine"
	"github.com/mrlm-net/simconnect/pkg/manager"
	"github.com/mrlm-net/simconnect/pkg/types"
)

// Client event IDs: arbitrary but must be unique within a connection.
const (
	eventAxisElevator  uint32 = 1
	eventAxisAileron   uint32 = 2
	eventAxisRudder    uint32 = 3
	eventGearToggle    uint32 = 4
	eventParkingBrake  uint32 = 5
	eventPauseSet      uint32 = 6
	eventThrottleEx1   uint32 = 7
	eventReverseToggle uint32 = 8

	notificationGroup uint32 = 1

	// AxisRange is the SimConnect AXIS_*_SET input range: full deflection
	// is ±AxisRange (0..AxisRange for throttle).
	AxisRange = 16383

	// Telemetry read: flap handle, gear position, throttle lever position,
	// parking brake state.
	defAircraftState uint32 = 100
	reqAircraftState uint32 = 200
)

// Client wraps a live, auto-reconnecting SimConnect connection. SetXxx
// calls made before MSFS2024 is actually reachable return an error rather
// than blocking — callers (control/simconnect) already treat backend
// errors as non-fatal, matching how a phone losing WiFi is handled.
type Client struct {
	mgr    manager.Manager
	log    *slog.Logger
	ready  atomic.Bool
	paused atomic.Bool
	state  atomic.Value // holds AircraftState
}

// aircraftStateData mirrors SimConnect's fixed struct layout for the
// telemetry read; field order must match setupTelemetryRead's
// AddToDataDefinition call order.
type aircraftStateData struct {
	FlapHandleIndex        int32
	FlapNumHandlePositions int32
	GearPositionPercent    int32
	ThrottleLeverPercent   int32
	ParkingBrakeSet        int32
}

type AircraftState struct {
	FlapHandleIndex        int32
	FlapNumHandlePositions int32
	// GearPositionPercent is 0 (fully up)..100 (fully down); values in
	// between mean the gear is in transit. Uses GEAR POSITION (percent),
	// not GEAR HANDLE POSITION (the commanded handle state, which can't
	// distinguish "down and locked" from "still retracting/extending") —
	// resolves a gap in an earlier approach that used GEAR HANDLE POSITION.
	// Not yet confirmed against a live sim.
	GearPositionPercent int32
	// ThrottleLeverPercent is GENERAL ENG THROTTLE LEVER POSITION:1,
	// -100..100 (negative = reverse) — the throttle lever's actual current
	// position in the sim, independent of what control/simconnect last
	// commanded. Feeds telemetry.Source so the phone can mirror manual
	// in-sim throttle changes instead of fighting them. Not yet confirmed
	// against a live sim.
	ThrottleLeverPercent int32
	// ParkingBrakeSet is BRAKE PARKING POSITION, 0 or 1 — same
	// don't-guess-locally rationale as ThrottleLeverPercent, applied to a
	// toggle button instead of a continuous axis: the app can show whether
	// the brake is actually set rather than assuming it never changes
	// outside a button tap. Not yet confirmed against a live sim.
	ParkingBrakeSet int32
}

// LatestState returns the most recent aircraft-state reading, or false if
// none has arrived yet (not connected, or MSFS2024 hasn't sent one).
func (c *Client) LatestState() (AircraftState, bool) {
	v := c.state.Load()
	if v == nil {
		return AircraftState{}, false
	}
	return v.(AircraftState), true
}

// Paused reports the sim's actual current pause state, from SimConnect's
// own "Pause" system event (github.com/mrlm-net/simconnect's
// manager.Manager.OnPause) — not a locally-guessed toggle. Defaults false
// before the first event arrives, matching the app's prior assumption.
func (c *Client) Paused() bool {
	return c.paused.Load()
}

// Connect starts the SimConnect manager in the background and returns
// immediately; it does not wait for MSFS2024 to be reachable — auto-
// reconnect (sim not running yet / paused / restarted) is handled
// internally, confirmed against a live sim.
func Connect(ctx context.Context, log *slog.Logger) *Client {
	c := &Client{log: log}

	c.mgr = manager.New("mHOTAS Server",
		manager.WithContext(ctx),
		manager.WithAutoReconnect(true),
		manager.WithAutoDetect(), // SimConnect.dll isn't always at the library's hardcoded default path
	)

	_ = c.mgr.OnConnectionStateChange(func(oldState, newState manager.ConnectionState) {
		c.log.Info("simconnect state change", "from", oldState, "to", newState)
		switch newState {
		case manager.StateConnected:
			if client := c.mgr.Client(); client != nil {
				setupEvents(client)
				setupTelemetryRead(client)
				c.ready.Store(true)
			}
		case manager.StateAvailable:
			// Event mapping already happened on StateConnected; nothing more needed.
		default:
			c.ready.Store(false)
		}
	})

	sub := c.mgr.Subscribe("mhotas-telemetry", 64)
	go c.watchTelemetry(sub.Messages())

	// SimConnect's own Pause system event, auto-subscribed by the manager
	// on connect — ground truth for Client.Paused(), not a locally-tracked
	// guess of whether our last SetPause call should have flipped it.
	_ = c.mgr.OnPause(func(paused bool) { c.paused.Store(paused) })

	go func() {
		if err := c.mgr.Start(); err != nil && !errors.Is(err, context.Canceled) {
			c.log.Error("simconnect manager stopped", "err", err)
		}
	}()

	return c
}

// setupEvents maps our client event IDs to MSFS's real events and
// registers them in a notification group, as required before
// TransmitClientEvent will do anything. Event names verified against the
// official MSFS SDK docs and confirmed against a live sim.
func setupEvents(client engine.Client) {
	client.MapClientEventToSimEvent(eventAxisElevator, "AXIS_ELEVATOR_SET")
	client.MapClientEventToSimEvent(eventAxisAileron, "AXIS_AILERONS_SET")
	client.MapClientEventToSimEvent(eventAxisRudder, "AXIS_RUDDER_SET")
	client.MapClientEventToSimEvent(eventGearToggle, "GEAR_TOGGLE")
	client.MapClientEventToSimEvent(eventParkingBrake, "PARKING_BRAKES")
	client.MapClientEventToSimEvent(eventPauseSet, "PAUSE_SET")
	client.MapClientEventToSimEvent(eventThrottleEx1, "THROTTLE_AXIS_SET_EX1")
	client.MapClientEventToSimEvent(eventReverseToggle, "THROTTLE_REVERSE_THRUST_TOGGLE")

	for _, id := range []uint32{
		eventAxisElevator, eventAxisAileron, eventAxisRudder,
		eventGearToggle, eventParkingBrake, eventPauseSet,
		eventThrottleEx1, eventReverseToggle,
	} {
		client.AddClientEventToNotificationGroup(notificationGroup, id, false)
	}
	client.SetNotificationGroupPriority(notificationGroup, 1)
}

// setupTelemetryRead registers the aircraft-state data definition and
// requests updates once per second.
func setupTelemetryRead(client engine.Client) {
	client.AddToDataDefinition(defAircraftState, "FLAPS HANDLE INDEX", "number", types.SIMCONNECT_DATATYPE_INT32, 0, 0)
	client.AddToDataDefinition(defAircraftState, "FLAPS NUM HANDLE POSITIONS", "number", types.SIMCONNECT_DATATYPE_INT32, 0, 1)
	client.AddToDataDefinition(defAircraftState, "GEAR POSITION", "percent", types.SIMCONNECT_DATATYPE_INT32, 0, 2)
	client.AddToDataDefinition(defAircraftState, "GENERAL ENG THROTTLE LEVER POSITION:1", "percent", types.SIMCONNECT_DATATYPE_INT32, 0, 3)
	client.AddToDataDefinition(defAircraftState, "BRAKE PARKING POSITION", "bool", types.SIMCONNECT_DATATYPE_INT32, 0, 4)

	client.RequestDataOnSimObject(
		reqAircraftState, defAircraftState,
		types.SIMCONNECT_OBJECT_ID_USER,
		types.SIMCONNECT_PERIOD_SECOND,
		types.SIMCONNECT_DATA_REQUEST_FLAG_DEFAULT,
		0, 0, 0,
	)
}

// watchTelemetry drains the telemetry subscription set up in Connect,
// storing each aircraft-state update for LatestState to serve — same
// latest-value-wins approach as the rest of this protocol, so a slow
// consumer just sees the newest reading rather than a backlog.
func (c *Client) watchTelemetry(messages <-chan engine.Message) {
	for msg := range messages {
		if msg.Err != nil {
			c.log.Warn("simconnect telemetry message error", "err", msg.Err)
			continue
		}
		if types.SIMCONNECT_RECV_ID(msg.DwID) != types.SIMCONNECT_RECV_ID_SIMOBJECT_DATA {
			continue
		}
		data := msg.AsSimObjectData()
		if uint32(data.DwRequestID) != reqAircraftState {
			continue
		}
		as := *engine.CastDataAs[aircraftStateData](&data.DwData)
		c.state.Store(AircraftState{
			FlapHandleIndex:        as.FlapHandleIndex,
			FlapNumHandlePositions: as.FlapNumHandlePositions,
			GearPositionPercent:    as.GearPositionPercent,
			ThrottleLeverPercent:   as.ThrottleLeverPercent,
			ParkingBrakeSet:        as.ParkingBrakeSet,
		})
	}
}

func (c *Client) transmit(eventID uint32, data uint32) error {
	if !c.ready.Load() {
		return fmt.Errorf("simlink: not connected to MSFS2024 yet")
	}
	client := c.mgr.Client()
	if client == nil {
		return fmt.Errorf("simlink: not connected to MSFS2024 yet")
	}
	return client.TransmitClientEvent(types.SIMCONNECT_OBJECT_ID_USER, eventID, data, notificationGroup, 0)
}

func (c *Client) transmitEx1(eventID uint32, data0 uint32) error {
	if !c.ready.Load() {
		return fmt.Errorf("simlink: not connected to MSFS2024 yet")
	}
	client := c.mgr.Client()
	if client == nil {
		return fmt.Errorf("simlink: not connected to MSFS2024 yet")
	}
	return client.TransmitClientEventEx1(types.SIMCONNECT_OBJECT_ID_USER, eventID, notificationGroup,
		types.SIMCONNECT_EVENT_FLAG_DEFAULT, [5]uint32{data0, 0, 0, 0, 0})
}

// SetElevator sends a raw AXIS_ELEVATOR_SET value, range -AxisRange..AxisRange.
func (c *Client) SetElevator(raw int32) error { return c.transmit(eventAxisElevator, uint32(raw)) }

// SetAileron sends a raw AXIS_AILERONS_SET value, range -AxisRange..AxisRange.
func (c *Client) SetAileron(raw int32) error { return c.transmit(eventAxisAileron, uint32(raw)) }

// SetThrottle sends a raw THROTTLE_AXIS_SET_EX1 value, range
// -AxisRange..AxisRange (idle..full power), computed by
// control/simconnect/throttle.go's toThrottleRaw — same event and range
// for both forward and reverse (see ToggleReverseThrust): once armed, MSFS
// applies this same idle..full magnitude as negative/reverse power
// internally, not because we send a negative raw value ourselves. Uses
// EX1 exclusively, not the plain AXIS_THROTTLE_SET event, on purpose —
// mixing the two left the engine stuck unable to return to forward thrust
// after a reverse pull (confirmed live on a Boeing-like jet); a single
// consistent write path avoids that.
func (c *Client) SetThrottle(raw int32) error { return c.transmitEx1(eventThrottleEx1, uint32(raw)) }

// ToggleReverseThrust fires THROTTLE_REVERSE_THRUST_TOGGLE, which arms or
// disarms reverse thrust for THROTTLE_AXIS_SET_EX1 (each call flips the
// state — call on a sign transition, not continuously). Confirmed by an
// Asobo developer on the MSFS DevSupport forum (devsupport.flightsimulator
// .com/t/throttle-set-event-ids-explanation/4571): "SET_EX supports
// reverse thrust (after setting KEY_THROTTLE_REVERSE_THRUST_TOGGLE)... the
// value of THROTTLE1_AXIS_SET_EX1 will be -1" — this toggle is EX1's
// actual documented arm mechanism, not the separate/older
// SET_REVERSE_THRUST_ON/OFF flag event (which doesn't reconcile with EX1
// and was the earlier cause of a stuck-in-reverse state).
func (c *Client) ToggleReverseThrust() error { return c.transmit(eventReverseToggle, 0) }

// SetRudder sends a raw AXIS_RUDDER_SET value, range -AxisRange..AxisRange.
func (c *Client) SetRudder(raw int32) error { return c.transmit(eventAxisRudder, uint32(raw)) }

// ToggleGear fires GEAR_TOGGLE.
func (c *Client) ToggleGear() error { return c.transmit(eventGearToggle, 0) }

// ToggleParkingBrake fires PARKING_BRAKES.
func (c *Client) ToggleParkingBrake() error { return c.transmit(eventParkingBrake, 0) }

// SetPause fires PAUSE_SET with an explicit target (1 = pause, 0 = resume),
// so the pilot can pause the sim from the phone (e.g. before setting it
// down) without touching the PC. Deliberately not PAUSE_TOGGLE: confirmed
// live (and matching widely-reported community/DevSupport issues) that
// PAUSE_TOGGLE reaches SimConnect with no error but MSFS2024 doesn't
// honor it when sent via TransmitClientEvent — the documented, parameterized
// PAUSE_SET is the reliable path, same lesson as
// THROTTLE_AXIS_SET_EX1/THROTTLE_REVERSE_THRUST_TOGGLE over the older
// ambiguous toggle mechanism. Callers compute the target from Client.Paused()
// (the real, telemetry-verified state), not a locally-tracked guess.
func (c *Client) SetPause(paused bool) error {
	var data uint32
	if paused {
		data = 1
	}
	return c.transmit(eventPauseSet, data)
}

func (c *Client) Close() error {
	return c.mgr.Stop()
}
