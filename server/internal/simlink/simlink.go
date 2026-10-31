//go:build windows

// Package simlink isolates the third-party SimConnect binding so a swap does not ripple into the rest of the server.
package simlink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/mrlm-net/simconnect/pkg/engine"
	"github.com/mrlm-net/simconnect/pkg/manager"
	"github.com/mrlm-net/simconnect/pkg/types"
)

const (
	eventPauseSet       uint32 = 6
	eventThrottleEx1    uint32 = 7
	eventReverseToggle  uint32 = 8
	eventAxisLeftBrake  uint32 = 11
	eventAxisRightBrake uint32 = 12
	eventSpoilersArmSet uint32 = 15

	notificationGroup uint32 = 1

	// AxisRange is the SimConnect AXIS_*_SET input range: full deflection
	// is +-AxisRange (0..AxisRange for throttle).
	AxisRange = 16383

	defAircraftState uint32 = 100
	reqAircraftState uint32 = 200

	// Must be in SimConnect's user range (1-999,999,849).
	reqInputEvents uint32 = 400

	// First client event ID handed out by RegisterEvent, clear of the fixed events above.
	genericEventBase uint32 = 1000
)

// Client wraps an auto-reconnecting SimConnect connection. Writes made
// before MSFS2024 is reachable return an error instead of blocking.
type Client struct {
	mgr    manager.Manager
	log    *slog.Logger
	ready  atomic.Bool
	paused atomic.Bool
	state  atomic.Value // holds AircraftState

	genericEventsMu sync.Mutex
	genericEvents   map[string]uint32
	nextGenericID   uint32

	// Input Event hashes are session-scoped: rebuilt on every connect, never reused across reconnects or aircraft changes.
	inputEventsMu sync.Mutex
	inputEvents   map[string]uint64

	// Single source of truth for THROTTLE_REVERSE_THRUST_TOGGLE's arm state.
	// The event only flips, so callers guessing their own state would desync each other.
	reverseArmedMu sync.Mutex
	reverseArmed   bool
}

// AircraftState mirrors SimConnect's struct layout for the telemetry read;
// field order must match setupTelemetryRead's AddToDataDefinition order.
type AircraftState struct {
	FlapHandleIndex        int32
	FlapNumHandlePositions int32
	// GearPositionPercent is 0 (up)..100 (down); in between is in transit.
	// GEAR HANDLE POSITION can't tell "down and locked" from "still extending".
	GearPositionPercent int32
	// ThrottleLeverPercent is the lever's actual position, -100..100 (negative = reverse).
	ThrottleLeverPercent  float32
	ParkingBrakeSet       int32
	StallWarning          int32
	OnGround              int32
	GroundVelocityKnots   float32
	SpoilersHandlePercent float32
	SpoilersArmed         int32
	ElevatorTrim          float32 // -1..1
	AutopilotMaster       int32
	// Armed alone doesn't mean the autothrottle is driving: a 737's A/T
	// switch stays armed all flight.
	AutothrottleArmed     int32
	ManagedThrottleActive int32 // Airbus A/THR
	AirspeedHold          int32
	MachHold              int32
}

// LatestState returns the most recent reading, or false if none has arrived yet.
func (c *Client) LatestState() (AircraftState, bool) {
	v := c.state.Load()
	if v == nil {
		return AircraftState{}, false
	}
	return v.(AircraftState), true
}

// Paused reports the sim's pause state from SimConnect's "Pause" system event.
func (c *Client) Paused() bool {
	return c.paused.Load()
}

// Ready reports whether the SimConnect handshake has completed.
func (c *Client) Ready() bool {
	return c.ready.Load()
}

// Connect starts the SimConnect manager in the background and returns immediately, before MSFS2024 is reachable.
func Connect(ctx context.Context, log *slog.Logger) *Client {
	c := &Client{log: log, genericEvents: map[string]uint32{}, nextGenericID: genericEventBase}

	c.mgr = manager.New("mHOTAS Server",
		manager.WithContext(ctx),
		manager.WithAutoReconnect(true),
		dllOption(log),
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
					// Expected on MSFS2020; Input Events are MSFS2024-only.
					c.log.Info("input event enumeration unavailable", "err", err)
				}
				c.ready.Store(true)
			}
		case manager.StateAvailable:
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

	_ = c.mgr.OnPause(func(paused bool) { c.paused.Store(paused) })

	go func() {
		if err := c.mgr.Start(); err != nil && !errors.Is(err, context.Canceled) {
			c.log.Error("simconnect manager stopped", "err", err)
		}
	}()

	return c
}

func setupEvents(client engine.Client) {
	for id, name := range map[uint32]string{
		eventPauseSet:       "PAUSE_SET",
		eventThrottleEx1:    "THROTTLE_AXIS_SET_EX1",
		eventReverseToggle:  "THROTTLE_REVERSE_THRUST_TOGGLE",
		eventAxisLeftBrake:  "AXIS_LEFT_BRAKE_SET",
		eventAxisRightBrake: "AXIS_RIGHT_BRAKE_SET",
		eventSpoilersArmSet: "SPOILERS_ARM_SET",
	} {
		client.MapClientEventToSimEvent(id, name)
		client.AddClientEventToNotificationGroup(notificationGroup, id, false)
	}
	client.SetNotificationGroupPriority(notificationGroup, 1)
}

// RegisterEvent assigns a stable client event ID to a K:EVENT name. Safe
// before the first connection: the actual mapping happens in
// setupGenericEvents on every connect.
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

// setupGenericEvents re-maps every registered name after a (re)connect.
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

// TransmitNamed fires a RegisterEvent-ed K:EVENT by name.
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
// Event, or false if the enumeration hasn't listed it (yet).
func (c *Client) ResolveInputEvent(name string) (uint64, bool) {
	c.inputEventsMu.Lock()
	defer c.inputEventsMu.Unlock()
	hash, ok := c.inputEvents[name]
	return hash, ok
}

// SetInputEvent writes a DOUBLE-typed Input Event by name (MSFS2024 only, fire-and-forget).
func (c *Client) SetInputEvent(name string, value float64) error {
	hash, ok := c.ResolveInputEvent(name)
	if !ok {
		return fmt.Errorf("simlink: input event %q not found in this session's enumeration", name)
	}
	if !c.ready.Load() {
		return errNotConnected
	}
	return c.mgr.SetInputEventDouble(hash, value)
}

// watchInputEvents caches name->hash from the enumeration responses.
// RgData is a single-element sentinel in the binding's Go struct; the real
// DwArraySize entries follow it in the response buffer, so they are walked
// with unsafe pointer arithmetic (indexing past RgData[0] would panic).
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

		// The wire stride can differ from the Go struct's size (alignment
		// padding). A fixed-size walk corrupted entries and segfaulted past
		// the buffer on a large airliner add-on, so derive the stride from
		// the message size and bounds-check every read.
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

// setupTelemetryRead requests the aircraft state on every sim frame where a
// value changed; 1Hz polling showed in-sim changes up to a second late.
// telemetry.Source caps the send rate.
func setupTelemetryRead(client engine.Client) {
	client.AddToDataDefinition(defAircraftState, "FLAPS HANDLE INDEX", "number", types.SIMCONNECT_DATATYPE_INT32, 0, 0)
	client.AddToDataDefinition(defAircraftState, "FLAPS NUM HANDLE POSITIONS", "number", types.SIMCONNECT_DATATYPE_INT32, 0, 1)
	client.AddToDataDefinition(defAircraftState, "GEAR POSITION", "percent", types.SIMCONNECT_DATATYPE_INT32, 0, 2)
	client.AddToDataDefinition(defAircraftState, "GENERAL ENG THROTTLE LEVER POSITION:1", "percent", types.SIMCONNECT_DATATYPE_FLOAT32, 0, 3)
	client.AddToDataDefinition(defAircraftState, "BRAKE PARKING POSITION", "bool", types.SIMCONNECT_DATATYPE_INT32, 0, 4)
	client.AddToDataDefinition(defAircraftState, "STALL WARNING", "bool", types.SIMCONNECT_DATATYPE_INT32, 0, 5)
	client.AddToDataDefinition(defAircraftState, "SIM ON GROUND", "bool", types.SIMCONNECT_DATATYPE_INT32, 0, 6)
	client.AddToDataDefinition(defAircraftState, "GROUND VELOCITY", "knots", types.SIMCONNECT_DATATYPE_FLOAT32, 0, 7)
	client.AddToDataDefinition(defAircraftState, "SPOILERS HANDLE POSITION", "percent", types.SIMCONNECT_DATATYPE_FLOAT32, 0, 8)
	client.AddToDataDefinition(defAircraftState, "SPOILERS ARMED", "bool", types.SIMCONNECT_DATATYPE_INT32, 0, 9)
	client.AddToDataDefinition(defAircraftState, "ELEVATOR TRIM PCT", "percent over 100", types.SIMCONNECT_DATATYPE_FLOAT32, 0, 10)
	client.AddToDataDefinition(defAircraftState, "AUTOPILOT MASTER", "bool", types.SIMCONNECT_DATATYPE_INT32, 0, 11)
	client.AddToDataDefinition(defAircraftState, "AUTOPILOT THROTTLE ARM", "bool", types.SIMCONNECT_DATATYPE_INT32, 0, 12)
	client.AddToDataDefinition(defAircraftState, "AUTOPILOT MANAGED THROTTLE ACTIVE", "bool", types.SIMCONNECT_DATATYPE_INT32, 0, 13)
	client.AddToDataDefinition(defAircraftState, "AUTOPILOT AIRSPEED HOLD", "bool", types.SIMCONNECT_DATATYPE_INT32, 0, 14)
	client.AddToDataDefinition(defAircraftState, "AUTOPILOT MACH HOLD", "bool", types.SIMCONNECT_DATATYPE_INT32, 0, 15)

	client.RequestDataOnSimObject(
		reqAircraftState, defAircraftState,
		types.SIMCONNECT_OBJECT_ID_USER,
		types.SIMCONNECT_PERIOD_SIM_FRAME,
		types.SIMCONNECT_DATA_REQUEST_FLAG_CHANGED,
		0, 0, 0,
	)
}

// watchTelemetry stores each reading for LatestState; a slow consumer sees the newest, not a backlog.
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
		c.state.Store(*engine.CastDataAs[AircraftState](&data.DwData))
	}
}

var errNotConnected = errors.New("simlink: not connected to MSFS2024 yet")

func (c *Client) connected() (engine.Client, error) {
	if !c.ready.Load() {
		return nil, errNotConnected
	}
	client := c.mgr.Client()
	if client == nil {
		return nil, errNotConnected
	}
	return client, nil
}

func (c *Client) transmit(eventID, data uint32) error {
	client, err := c.connected()
	if err != nil {
		return err
	}
	return client.TransmitClientEvent(types.SIMCONNECT_OBJECT_ID_USER, eventID, data, notificationGroup, 0)
}

func (c *Client) transmitEx1(eventID, data0 uint32) error {
	client, err := c.connected()
	if err != nil {
		return err
	}
	return client.TransmitClientEventEx1(types.SIMCONNECT_OBJECT_ID_USER, eventID, notificationGroup,
		types.SIMCONNECT_EVENT_FLAG_DEFAULT, [5]uint32{data0, 0, 0, 0, 0})
}

// SetThrottle sends a raw THROTTLE_AXIS_SET_EX1 value, -AxisRange..AxisRange
// (idle..full power). Reverse uses the same magnitude once armed via
// SetReverseArmed. EX1 only: mixing it with AXIS_THROTTLE_SET left the
// engine stuck unable to return to forward thrust after a reverse pull.
func (c *Client) SetThrottle(raw int32) error { return c.transmitEx1(eventThrottleEx1, uint32(raw)) }

// SetReverseArmed brings THROTTLE_REVERSE_THRUST_TOGGLE to the wanted arm
// state, firing only on a real transition. This toggle is EX1's documented
// reverse mechanism; the older SET_REVERSE_THRUST_ON/OFF events don't
// reconcile with EX1 and caused a stuck-in-reverse state.
func (c *Client) SetReverseArmed(want bool) error {
	c.reverseArmedMu.Lock()
	defer c.reverseArmedMu.Unlock()
	if want == c.reverseArmed {
		return nil
	}
	if err := c.transmit(eventReverseToggle, 0); err != nil {
		return err
	}
	c.reverseArmed = want
	return nil
}

// SetToeBrakes drives both brake axes to full or fully released. Not
// proportional: the axes are non-linear and asymmetric. Raw 0 is the
// midpoint (half applied), so release must send -AxisRange.
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

// SetPause sets pause explicitly. PAUSE_TOGGLE reaches SimConnect without
// error but MSFS2024 ignores it via TransmitClientEvent.
func (c *Client) SetPause(paused bool) error {
	var data uint32
	if paused {
		data = 1
	}
	return c.transmit(eventPauseSet, data)
}

// SetSpoilersArmed sets the arm state explicitly. SPOILERS_ARM_TOGGLE is
// reported to arm but fail to disarm.
func (c *Client) SetSpoilersArmed(armed bool) error {
	var data uint32
	if armed {
		data = 1
	}
	return c.transmit(eventSpoilersArmSet, data)
}

func (c *Client) Close() error { return c.mgr.Stop() }

// dllOption prefers the SimConnect.dll shipped next to the exe in release
// builds, so testers don't need the MSFS SDK. SIMCONNECT_DLL still overrides
// it, and without a bundled copy the library searches the SDK install paths.
func dllOption(log *slog.Logger) manager.Option {
	if os.Getenv("SIMCONNECT_DLL") == "" {
		if exe, err := os.Executable(); err == nil {
			bundled := filepath.Join(filepath.Dir(exe), "SimConnect.dll")
			if _, err := os.Stat(bundled); err == nil {
				log.Info("using bundled SimConnect.dll", "path", bundled)
				return manager.WithDLLPath(bundled)
			}
		}
	}
	return manager.WithAutoDetect()
}

// probeIDs hands each ReadOnce its own definition/request ID, clear of the
// fixed IDs above.
var probeIDs atomic.Uint32

// ReadOnce reads arbitrary simvars or L:vars (e.g. "L:A32NX_FLAPS_HANDLE_INDEX")
// once, as float64 in the given units. For dev probing, not the hot path.
func (c *Client) ReadOnce(ctx context.Context, names, units []string) ([]float64, error) {
	client, err := c.connected()
	if err != nil {
		return nil, err
	}
	id := 500_000 + probeIDs.Add(1)
	sub := c.mgr.SubscribeWithFilter(fmt.Sprintf("probe-%d", id), 4, func(msg engine.Message) bool {
		return types.SIMCONNECT_RECV_ID(msg.DwID) == types.SIMCONNECT_RECV_ID_SIMOBJECT_DATA &&
			uint32(msg.AsSimObjectData().DwRequestID) == id
	})
	defer sub.Unsubscribe()

	for i, name := range names {
		client.AddToDataDefinition(id, name, units[i], types.SIMCONNECT_DATATYPE_FLOAT64, 0, uint32(i))
	}
	client.RequestDataOnSimObject(id, id, types.SIMCONNECT_OBJECT_ID_USER, types.SIMCONNECT_PERIOD_ONCE, 0, 0, 0, 0)

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("simlink: no reply (unknown var or unit?): %w", ctx.Err())
	case msg, ok := <-sub.Messages():
		if !ok {
			return nil, errNotConnected
		}
		if msg.Err != nil {
			return nil, msg.Err
		}
		data := msg.AsSimObjectData()
		return append([]float64(nil), unsafe.Slice((*float64)(unsafe.Pointer(&data.DwData)), len(names))...), nil
	}
}
