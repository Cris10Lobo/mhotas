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
	"maps"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/mrlm-net/simconnect/pkg/engine"
	"github.com/mrlm-net/simconnect/pkg/manager"
	"github.com/mrlm-net/simconnect/pkg/types"
)

// Client event IDs: arbitrary but must be unique within a connection.
const (
	eventAxisElevator   uint32 = 1
	eventAxisAileron    uint32 = 2
	eventAxisRudder     uint32 = 3
	eventGearToggle     uint32 = 4
	eventParkingBrake   uint32 = 5
	eventPauseSet       uint32 = 6
	eventThrottleEx1    uint32 = 7
	eventReverseToggle  uint32 = 8
	eventFlapsIncr      uint32 = 9
	eventFlapsDecr      uint32 = 10
	eventAxisLeftBrake  uint32 = 11
	eventAxisRightBrake uint32 = 12
	eventSpoilersInc    uint32 = 13
	eventSpoilersDec    uint32 = 14
	eventSpoilersArmSet uint32 = 15
	eventAutopilotOff   uint32 = 16

	notificationGroup uint32 = 1

	// AxisRange is the SimConnect AXIS_*_SET input range: full deflection
	// is ±AxisRange (0..AxisRange for throttle).
	AxisRange = 16383

	// Telemetry read: flap handle, gear position, throttle lever position,
	// parking brake state.
	defAircraftState uint32 = 100
	reqAircraftState uint32 = 200

	// reqInputEvents is EnumerateInputEvents' request ID — must be in
	// SimConnect's "user range" (1–999,999,849), clear of the fixed pair
	// above.
	reqInputEvents uint32 = 400

	// genericEventBase is the first client event ID handed out by
	// RegisterEvent, for config-declared K:EVENT names beyond the fixed set
	// above — clear of both that set and the telemetry request IDs.
	genericEventBase uint32 = 1000
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

	// genericEvents backs RegisterEvent/TransmitNamed: config-declared
	// K:EVENT names, assigned a stable client event ID up front (safe to
	// call before the first connection) and (re-)mapped to that ID inside
	// setupEvents on every StateConnected, same as the fixed events above.
	genericEventsMu sync.Mutex
	genericEvents   map[string]uint32
	nextGenericID   uint32

	// inputEvents caches the current session's Input Event name->hash
	// enumeration (see watchInputEvents) — cleared and rebuilt on every
	// StateConnected, since hashes are session-scoped and must never be
	// reused across a reconnect or aircraft change (confirmed in the
	// mrlm-net/simconnect manager docs).
	inputEventsMu sync.Mutex
	inputEvents   map[string]uint64
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
	// The 4 fields below are continuous simvars SimConnect reports as
	// doubles, unlike the naturally-integer index/percent fields above.
	AirspeedKnots    float64
	AltitudeFeet     float64
	HeadingDegrees   float64
	VerticalSpeedFpm float64
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
	// AirspeedKnots is AIRSPEED INDICATED, knots. Feeds a configurable
	// status tile rather than any control path. Not yet confirmed against
	// a live sim.
	AirspeedKnots float64
	// AltitudeFeet is INDICATED ALTITUDE, feet.
	AltitudeFeet float64
	// HeadingDegrees is PLANE HEADING DEGREES MAGNETIC, degrees.
	HeadingDegrees float64
	// VerticalSpeedFpm is VERTICAL SPEED, feet/minute.
	VerticalSpeedFpm float64
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

// Ready reports whether the SimConnect handshake has completed and events/
// telemetry are actually flowing — false while waiting for MSFS2024 to
// launch or reconnecting after it closes.
func (c *Client) Ready() bool {
	return c.ready.Load()
}

// Connect starts the SimConnect manager in the background and returns
// immediately; it does not wait for MSFS2024 to be reachable — auto-
// reconnect (sim not running yet / paused / restarted) is handled
// internally, confirmed against a live sim.
func Connect(ctx context.Context, log *slog.Logger) *Client {
	c := &Client{log: log, genericEvents: map[string]uint32{}, nextGenericID: genericEventBase}

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
				c.setupGenericEvents(client)
				setupTelemetryRead(client)
				c.inputEventsMu.Lock()
				c.inputEvents = map[string]uint64{}
				c.inputEventsMu.Unlock()
				if err := c.mgr.EnumerateInputEvents(reqInputEvents); err != nil {
					// Expected on MSFS2020 (Input Events are MSFS2024-only)
					// and harmless either way — nothing depends on this
					// succeeding yet (diagnostic + input_event driver
					// groundwork only).
					c.log.Info("input event enumeration unavailable", "err", err)
				}
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

	inputEventsSub := c.mgr.SubscribeWithFilter("mhotas-input-events", 50, func(msg engine.Message) bool {
		return types.SIMCONNECT_RECV_ID(msg.DwID) == types.SIMCONNECT_RECV_ID_ENUMERATE_INPUT_EVENTS
	})
	go c.watchInputEvents(inputEventsSub.Messages())

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
	client.MapClientEventToSimEvent(eventFlapsIncr, "FLAPS_INCR")
	client.MapClientEventToSimEvent(eventFlapsDecr, "FLAPS_DECR")
	client.MapClientEventToSimEvent(eventAxisLeftBrake, "AXIS_LEFT_BRAKE_SET")
	client.MapClientEventToSimEvent(eventAxisRightBrake, "AXIS_RIGHT_BRAKE_SET")
	client.MapClientEventToSimEvent(eventSpoilersInc, "SPOILERS_INC")
	client.MapClientEventToSimEvent(eventSpoilersDec, "SPOILERS_DEC")
	client.MapClientEventToSimEvent(eventSpoilersArmSet, "SPOILERS_ARM_SET")
	client.MapClientEventToSimEvent(eventAutopilotOff, "AUTOPILOT_OFF")

	for _, id := range []uint32{
		eventAxisElevator, eventAxisAileron, eventAxisRudder,
		eventGearToggle, eventParkingBrake, eventPauseSet,
		eventThrottleEx1, eventReverseToggle,
		eventFlapsIncr, eventFlapsDecr,
		eventAxisLeftBrake, eventAxisRightBrake,
		eventSpoilersInc, eventSpoilersDec, eventSpoilersArmSet,
		eventAutopilotOff, // was missing from this loop before the AP-disconnect button existed
	} {
		client.AddClientEventToNotificationGroup(notificationGroup, id, false)
	}
	client.SetNotificationGroupPriority(notificationGroup, 1)
}

// RegisterEvent assigns a stable client event ID to an arbitrary SimConnect
// K:EVENT name, for control/simconnect's generic "kevent" driver — a config-
// declared event this project has no dedicated Client method for. Safe to
// call at driver-construction time, before the first connection completes:
// this only reserves bookkeeping, actual mapping happens in
// setupGenericEvents on every StateConnected (mirroring how the fixed events
// above are (re-)mapped on every reconnect, not just the first one).
func (c *Client) RegisterEvent(name string) uint32 {
	c.genericEventsMu.Lock()
	defer c.genericEventsMu.Unlock()
	if id, ok := c.genericEvents[name]; ok {
		return id
	}
	id := c.nextGenericID
	c.nextGenericID++
	c.genericEvents[name] = id
	return id
}

// setupGenericEvents (re-)maps every name RegisterEvent has assigned an ID
// to, so a generic "kevent" driver built before the first connection (or
// still holding IDs from a previous SimConnect session) keeps working after
// a reconnect.
func (c *Client) setupGenericEvents(client engine.Client) {
	c.genericEventsMu.Lock()
	snapshot := make(map[string]uint32, len(c.genericEvents))
	maps.Copy(snapshot, c.genericEvents)
	c.genericEventsMu.Unlock()

	for name, id := range snapshot {
		client.MapClientEventToSimEvent(id, name)
		client.AddClientEventToNotificationGroup(notificationGroup, id, false)
	}
}

// TransmitNamed fires a previously RegisterEvent-ed K:EVENT by name.
func (c *Client) TransmitNamed(name string, data uint32) error {
	c.genericEventsMu.Lock()
	id, ok := c.genericEvents[name]
	c.genericEventsMu.Unlock()
	if !ok {
		return fmt.Errorf("simlink: event %q was never registered", name)
	}
	return c.transmit(id, data)
}

// ResolveInputEvent returns the current session's hash for a named Input
// Event, or false if it hasn't been seen in this session's enumeration yet
// (not connected, this aircraft doesn't expose an event with this name, or
// the enumeration response just hasn't arrived yet).
func (c *Client) ResolveInputEvent(name string) (uint64, bool) {
	c.inputEventsMu.Lock()
	defer c.inputEventsMu.Unlock()
	hash, ok := c.inputEvents[name]
	return hash, ok
}

// SetInputEvent writes a DOUBLE-typed Input Event value by name, resolving
// it against the current session's enumeration (see ResolveInputEvent).
// MSFS2024-only; fire-and-forget like the classic TransmitClientEvent path
// (no confirmation the write actually landed).
func (c *Client) SetInputEvent(name string, value float64) error {
	hash, ok := c.ResolveInputEvent(name)
	if !ok {
		return fmt.Errorf("simlink: input event %q not found in this session's enumeration", name)
	}
	if !c.ready.Load() {
		return fmt.Errorf("simlink: not connected to MSFS2024 yet")
	}
	return c.mgr.SetInputEventDouble(hash, value)
}

// watchInputEvents drains the enumeration subscription set up in Connect,
// logging every descriptor (diagnostic — settles on real data what a given
// aircraft actually exposes) and caching name->hash so ResolveInputEvent has
// something to serve. SIMCONNECT_RECV_ENUMERATE_INPUT_EVENTS.RgData is a
// single-element sentinel array in this binding's Go struct — the real
// batch, DwArraySize entries long, follows it in the DLL's response buffer,
// so entries beyond RgData[0] are walked by hand via unsafe pointer
// arithmetic rather than by indexing the Go array (which would panic past
// its declared length of 1).
func (c *Client) watchInputEvents(messages <-chan engine.Message) {
	declaredSize := unsafe.Sizeof(types.SIMCONNECT_INPUT_EVENT_DESCRIPTOR{})
	headerSize := unsafe.Sizeof(types.SIMCONNECT_RECV_LIST_TEMPLATE{})
	for msg := range messages {
		if msg.Err != nil {
			c.log.Warn("input event enumeration message error", "err", msg.Err)
			continue
		}
		recv := msg.AsEnumerateInputEvents()
		if recv == nil || uint32(recv.DwRequestID) != reqInputEvents {
			continue
		}
		arraySize := int(recv.DwArraySize)
		if arraySize == 0 {
			continue
		}

		// The real per-entry wire stride doesn't necessarily match this
		// binding's declared SIMCONNECT_INPUT_EVENT_DESCRIPTOR size — hit
		// live against a real aircraft (a big airliner add-on) with a
		// naive fixed-size walk and it corrupted progressively worse
		// entry-by-entry before eventually segfaulting past the message
		// buffer. guide-facilities.md documents this exact class of
		// mismatch for a sibling list struct ("the Go struct has alignment
		// padding that does not match the SimConnect wire format") and
		// derives the real stride from the message size instead of
		// trusting unsafe.Sizeof — same fix applied here, plus a hard
		// bounds check on every read so a still-wrong stride guess can
		// never walk off the buffer again.
		base := unsafe.Pointer(recv)
		dataStart := unsafe.Add(base, headerSize)
		bufEnd := uintptr(base) + uintptr(msg.Size)
		stride := (bufEnd - uintptr(dataStart)) / uintptr(arraySize)
		if stride == 0 || stride > declaredSize {
			c.log.Warn("input event enumeration: unexpected entry stride, skipping batch",
				"stride", stride, "declared", declaredSize, "arraySize", arraySize, "msgSize", msg.Size)
			continue
		}

		found := make(map[string]uint64, arraySize)
		for i := range arraySize {
			entry := unsafe.Add(dataStart, uintptr(i)*stride)
			if uintptr(entry)+declaredSize > bufEnd {
				c.log.Warn("input event enumeration: entry would read past message buffer, stopping batch", "index", i)
				break
			}
			desc := (*types.SIMCONNECT_INPUT_EVENT_DESCRIPTOR)(entry)
			name := engine.BytesToString(desc.Name[:])
			hash := uint64(desc.Hash)
			found[name] = hash
			c.log.Info("input event", "name", name, "hash", hash, "type", desc.Type)
		}
		c.inputEventsMu.Lock()
		maps.Copy(c.inputEvents, found)
		c.inputEventsMu.Unlock()
	}
}

// setupTelemetryRead registers the aircraft-state data definition and
// requests updates once per second.
func setupTelemetryRead(client engine.Client) {
	client.AddToDataDefinition(defAircraftState, "FLAPS HANDLE INDEX", "number", types.SIMCONNECT_DATATYPE_INT32, 0, 0)
	client.AddToDataDefinition(defAircraftState, "FLAPS NUM HANDLE POSITIONS", "number", types.SIMCONNECT_DATATYPE_INT32, 0, 1)
	client.AddToDataDefinition(defAircraftState, "GEAR POSITION", "percent", types.SIMCONNECT_DATATYPE_INT32, 0, 2)
	client.AddToDataDefinition(defAircraftState, "GENERAL ENG THROTTLE LEVER POSITION:1", "percent", types.SIMCONNECT_DATATYPE_INT32, 0, 3)
	client.AddToDataDefinition(defAircraftState, "BRAKE PARKING POSITION", "bool", types.SIMCONNECT_DATATYPE_INT32, 0, 4)
	client.AddToDataDefinition(defAircraftState, "AIRSPEED INDICATED", "knots", types.SIMCONNECT_DATATYPE_FLOAT64, 0, 5)
	client.AddToDataDefinition(defAircraftState, "INDICATED ALTITUDE", "feet", types.SIMCONNECT_DATATYPE_FLOAT64, 0, 6)
	client.AddToDataDefinition(defAircraftState, "PLANE HEADING DEGREES MAGNETIC", "degrees", types.SIMCONNECT_DATATYPE_FLOAT64, 0, 7)
	client.AddToDataDefinition(defAircraftState, "VERTICAL SPEED", "feet/minute", types.SIMCONNECT_DATATYPE_FLOAT64, 0, 8)

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
			AirspeedKnots:          as.AirspeedKnots,
			AltitudeFeet:           as.AltitudeFeet,
			HeadingDegrees:         as.HeadingDegrees,
			VerticalSpeedFpm:       as.VerticalSpeedFpm,
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

// SetToeBrakes drives AXIS_LEFT_BRAKE_SET/AXIS_RIGHT_BRAKE_SET to full
// (applied) or fully released. Not a proportional control: both axes are
// documented as non-linear and asymmetric to each other, so there's no
// usable "brake X%" mapping — this only ever asks for max or nothing.
// Like AXIS_THROTTLE_SET (see throttle.go), the raw range is signed
// -AxisRange..AxisRange with 0 at the MIDPOINT, not the released end — 0
// leaves the brakes roughly half-applied, so a full release must send
// -AxisRange, not 0.
func (c *Client) SetToeBrakes(applied bool) error {
	raw := int32(-AxisRange)
	if applied {
		raw = AxisRange
	}
	if err := c.transmit(eventAxisLeftBrake, uint32(raw)); err != nil {
		return err
	}
	return c.transmit(eventAxisRightBrake, uint32(raw))
}

// ToggleGear fires GEAR_TOGGLE.
func (c *Client) ToggleGear() error { return c.transmit(eventGearToggle, 0) }

// ToggleParkingBrake fires PARKING_BRAKES.
func (c *Client) ToggleParkingBrake() error { return c.transmit(eventParkingBrake, 0) }

// IncrFlaps fires FLAPS_INCR, stepping the flap handle out one detent.
// There is no SimConnect event to set an exact flap handle index —
// FLAPS_SET only takes a 0..16383 "closest increment" value, an
// approximation rejected in favor of the same discrete per-detent taps a
// real flap handle notch requires.
func (c *Client) IncrFlaps() error { return c.transmit(eventFlapsIncr, 0) }

// DecrFlaps fires FLAPS_DECR, stepping the flap handle in one detent.
func (c *Client) DecrFlaps() error { return c.transmit(eventFlapsDecr, 0) }

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

// IncrSpoilers fires SPOILERS_INC. Experimental (milestone 14): no exact-
// detent "set spoiler position" event exists in SimConnect, so, same as
// flaps, deploy/retract is step-based rather than absolute — confirm this
// gives usable resolution once tested live.
func (c *Client) IncrSpoilers() error { return c.transmit(eventSpoilersInc, 0) }

// DecrSpoilers fires SPOILERS_DEC. See IncrSpoilers.
func (c *Client) DecrSpoilers() error { return c.transmit(eventSpoilersDec, 0) }

// SetSpoilersArmed fires SPOILERS_ARM_SET with an explicit target (1 =
// armed, 0 = disarmed). Deliberately not SPOILERS_ARM_TOGGLE: multiple
// independent community/DevSupport reports describe it arming successfully
// but failing to disarm — the same toggle-unreliability class as
// PAUSE_TOGGLE (see SetPause) — so this uses the explicit-value SET instead.
// Experimental: not yet confirmed against a live sim.
func (c *Client) SetSpoilersArmed(armed bool) error {
	var data uint32
	if armed {
		data = 1
	}
	return c.transmit(eventSpoilersArmSet, data)
}

// DisconnectAutopilot fires AUTOPILOT_OFF — confirmed via
// docs.flightsimulator.com's Aircraft Autopilot/Flight Assist Events page as
// a real, documented explicit-off event ("Turns AP off"). Deliberately not
// AP_MASTER or AUTOPILOT_DISENGAGE_TOGGLE: both are real too, but this
// project has hit the toggle-unreliability class of bug twice already
// (PAUSE_TOGGLE, SPOILERS_ARM_TOGGLE) — an explicit "off" event needs no
// state to get out of sync with and can't accidentally re-engage the AP the
// way a toggle could if fired twice (e.g. two of the 3x-redundant BUTTON
// packets landing on either side of a state change).
func (c *Client) DisconnectAutopilot() error { return c.transmit(eventAutopilotOff, 0) }

func (c *Client) Close() error {
	return c.mgr.Stop()
}
