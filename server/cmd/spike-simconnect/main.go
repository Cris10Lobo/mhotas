//go:build windows

// Command spike-simconnect is a throwaway program. It exists to answer one
// question before anything else in this project gets built: can
// github.com/mrlm-net/simconnect reliably (a) drive MSFS2024's flight
// controls via TransmitClientEvent, and (b) read live simvar state (flap
// handle index, gear handle position) back out.
//
// This program only builds on Windows (SimConnect requirement) and only
// does anything useful with MSFS2024 running — it cannot be exercised in
// this repo's non-Windows dev sandbox. Function/type names below were
// pulled from github.com/mrlm-net/simconnect's own documented examples
// (simconnect-subscribe, emit-events), not guessed, but the actual
// behavior against a live sim is exactly what this spike is for.
//
// Usage once running on the Windows dev PC with MSFS2024 loaded into an
// aircraft:
//
//	go run . 2>&1 | tee spike.log
//
// Then at the prompt, try commands like:
//
//	elevator -50      (pitch nose down, range -100..100)
//	aileron 30        (roll right, range -100..100)
//	throttle 75       (75% forward throttle, range 0..100)
//	rudder 0          (center)
//	quit
//
// Watch the aircraft respond in MSFS2024, and watch this program's log for
// live FLAPS HANDLE INDEX / GEAR HANDLE POSITION prints — change flaps and
// gear via keyboard in MSFS2024 and confirm the printed values track them.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/mrlm-net/simconnect/pkg/engine"
	"github.com/mrlm-net/simconnect/pkg/manager"
	"github.com/mrlm-net/simconnect/pkg/types"
)

// Client event IDs: arbitrary but must be unique within this program. Real
// event names ("AXIS_ELEVATOR_SET" etc.) are looked up by
// MapClientEventToSimEvent against MSFS's own event table.
const (
	eventAxisElevator uint32 = 1
	eventAxisAileron  uint32 = 2
	eventAxisThrottle uint32 = 3
	eventAxisRudder   uint32 = 4

	notificationGroup uint32 = 1

	// axisRange is the classic SimConnect AXIS_*_SET input range: full
	// deflection is ±axisRange (0..axisRange for throttle in this spike,
	// since reverse thrust isn't exercised here).
	axisRange = 16383
)

// flapGearData mirrors the fixed struct read via RequestDataOnSimObject
// below. Field order must match the AddToDataDefinition call order.
//
// CAUTION (flag for docs/protocol.md follow-up): GEAR HANDLE POSITION is
// the *commanded* handle state (0/1), not the mechanism's actual position
// — it cannot distinguish "gear down and locked" from "gear currently
// retracting/extending". docs/protocol.md's GearPosition enum has a
// Transit value that this simvar alone cannot populate accurately; a real
// transit read would need GEAR POSITION (percent, 0=up/100=down) per
// wheel instead. This spike deliberately uses the simpler handle-position
// read first since it directly parallels FLAPS HANDLE INDEX, and notes
// the gap rather than silently shipping an inaccurate "transit" state.
type flapGearData struct {
	FlapHandleIndex    int32
	FlapNumHandlePos   int32
	GearHandlePosition int32
}

const (
	defFlapGear uint32 = 100
	reqFlapGear uint32 = 200
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Println("\nshutting down...")
		cancel()
	}()

	mgr := manager.New("mHOTAS SimConnect Spike",
		manager.WithContext(ctx),
		manager.WithAutoReconnect(true), // sim not running / paused / restarted shouldn't crash this program
		manager.WithAutoDetect(),        // SimConnect.dll isn't always at the library's hardcoded default path
	)

	_ = mgr.OnConnectionStateChange(func(oldState, newState manager.ConnectionState) {
		fmt.Printf("state: %s -> %s\n", oldState, newState)
		if newState == manager.StateConnected {
			if client := mgr.Client(); client != nil {
				setupEvents(client)
				setupTelemetryRead(client)
			}
		}
	})

	sub := mgr.Subscribe("spike", 256)
	go handleMessages(sub.Messages())

	go runPrompt(ctx, mgr)

	fmt.Println("connecting to MSFS2024 (make sure it's running with an aircraft loaded)...")
	if err := mgr.Start(); err != nil && !errors.Is(err, context.Canceled) {
		// context.Canceled means "quit" or Ctrl+C triggered a clean
		// shutdown (runPrompt/the signal handler calls mgr.Stop(), which
		// cancels the context Start() is blocked on) — not a real failure.
		fmt.Fprintln(os.Stderr, "manager stopped:", err)
		os.Exit(1)
	}
}

// setupEvents maps our client event IDs to MSFS's real axis-set events and
// registers them in a notification group, as required before
// TransmitClientEvent will do anything.
func setupEvents(client engine.Client) {
	client.MapClientEventToSimEvent(eventAxisElevator, "AXIS_ELEVATOR_SET")
	client.MapClientEventToSimEvent(eventAxisAileron, "AXIS_AILERONS_SET")
	client.MapClientEventToSimEvent(eventAxisThrottle, "AXIS_THROTTLE_SET")
	client.MapClientEventToSimEvent(eventAxisRudder, "AXIS_RUDDER_SET")

	for _, id := range []uint32{eventAxisElevator, eventAxisAileron, eventAxisThrottle, eventAxisRudder} {
		client.AddClientEventToNotificationGroup(notificationGroup, id, false)
	}
	client.SetNotificationGroupPriority(notificationGroup, 1)

	fmt.Println("axis events mapped, ready for elevator/aileron/throttle/rudder commands")
}

// setupTelemetryRead registers the flap/gear data definition and requests
// updates once per second — this is the read-path half of the spike.
func setupTelemetryRead(client engine.Client) {
	client.AddToDataDefinition(defFlapGear, "FLAPS HANDLE INDEX", "number", types.SIMCONNECT_DATATYPE_INT32, 0, 0)
	client.AddToDataDefinition(defFlapGear, "FLAPS NUM HANDLE POSITIONS", "number", types.SIMCONNECT_DATATYPE_INT32, 0, 1)
	client.AddToDataDefinition(defFlapGear, "GEAR HANDLE POSITION", "bool", types.SIMCONNECT_DATATYPE_INT32, 0, 2)

	client.RequestDataOnSimObject(
		reqFlapGear, defFlapGear,
		types.SIMCONNECT_OBJECT_ID_USER,
		types.SIMCONNECT_PERIOD_SECOND,
		types.SIMCONNECT_DATA_REQUEST_FLAG_DEFAULT,
		0, 0, 0,
	)

	fmt.Println("telemetry read requested: FLAPS HANDLE INDEX, FLAPS NUM HANDLE POSITIONS, GEAR HANDLE POSITION (1Hz)")
}

func handleMessages(messages <-chan engine.Message) {
	var last flapGearData
	haveLast := false

	for msg := range messages {
		if msg.Err != nil {
			fmt.Fprintln(os.Stderr, "message error:", msg.Err)
			continue
		}
		switch types.SIMCONNECT_RECV_ID(msg.DwID) {
		case types.SIMCONNECT_RECV_ID_SIMOBJECT_DATA:
			data := msg.AsSimObjectData()
			if uint32(data.DwRequestID) != reqFlapGear {
				continue
			}
			fg := *engine.CastDataAs[flapGearData](&data.DwData)
			// Requested at 1Hz regardless of change, but printing every
			// sample floods the terminal (this data barely changes) and
			// buries the interactive prompt below — only print on change.
			if haveLast && fg == last {
				continue
			}
			last, haveLast = fg, true
			fmt.Printf("telemetry: flap=%d/%d gearHandle=%d\n",
				fg.FlapHandleIndex, fg.FlapNumHandlePos, fg.GearHandlePosition)
		case types.SIMCONNECT_RECV_ID_EXCEPTION:
			fmt.Fprintf(os.Stderr, "SimConnect exception: %+v\n", msg.AsException())
		}
	}
}

// runPrompt is the manual-sweep interface described in the package doc
// comment: type an axis name and a percent value, watch MSFS2024 respond.
func runPrompt(ctx context.Context, mgr manager.Manager) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if fields[0] == "quit" {
			mgr.Stop()
			return
		}
		if len(fields) != 2 {
			fmt.Println("usage: <elevator|aileron|throttle|rudder> <percent>  (or: quit)")
			continue
		}
		pct, err := strconv.Atoi(fields[1])
		if err != nil {
			fmt.Println("percent must be an integer:", err)
			continue
		}

		client := mgr.Client()
		if client == nil {
			fmt.Println("not connected yet")
			continue
		}

		var eventID uint32
		var value int32
		switch fields[0] {
		case "elevator":
			eventID, value = eventAxisElevator, clampAxis(pct)
		case "aileron":
			eventID, value = eventAxisAileron, clampAxis(pct)
		case "rudder":
			eventID, value = eventAxisRudder, clampAxis(pct)
		case "throttle":
			eventID, value = eventAxisThrottle, clampThrottle(pct)
		default:
			fmt.Println("unknown axis:", fields[0])
			continue
		}

		if err := client.TransmitClientEvent(
			types.SIMCONNECT_OBJECT_ID_USER, eventID, uint32(value), notificationGroup, 0,
		); err != nil {
			fmt.Println("TransmitClientEvent failed:", err)
			continue
		}
		fmt.Printf("sent %s = %d%% (raw %d)\n", fields[0], pct, value)
	}
}

// clampAxis maps a -100..100 percent to the SimConnect ±axisRange input range.
func clampAxis(pct int) int32 {
	if pct < -100 {
		pct = -100
	}
	if pct > 100 {
		pct = 100
	}
	return int32(pct * axisRange / 100)
}

// clampThrottle maps a 0..100 percent to the SimConnect 0..axisRange input
// range. Negative values (reverse thrust) aren't exercised by this spike.
func clampThrottle(pct int) int32 {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return int32(pct * axisRange / 100)
}
