//go:build windows

// Package vjoy implements control.Output by driving a vJoy virtual
// joystick. It writes only: what each vJoy axis/button does in the sim is
// whatever the user bound it to in MSFS's controls menu, and reading state
// back is telemetry's job, over SimConnect.
//
// The mapping is fixed rather than configurable — the MSFS binding is where
// a user decides what an input means, so remapping here too would just be
// a second place to get it wrong. vJoy button N is control.ButtonID N.
package vjoy

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"mhotas/internal/control"
	"mhotas/internal/vjoylink"
)

// ponytail: single hardcoded device — one phone, one PC.
const deviceID = 1

var axisUsage = map[control.AxisID]uint32{
	control.AxisPitch:    vjoylink.AxisY,
	control.AxisRoll:     vjoylink.AxisX,
	control.AxisThrottle: vjoylink.AxisZ,
	control.AxisRudder:   vjoylink.AxisRZ,
}

var axisLetter = map[uint32]string{
	vjoylink.AxisX: "X", vjoylink.AxisY: "Y", vjoylink.AxisZ: "Z", vjoylink.AxisRZ: "Rz",
}

// Sim is the SimConnect read connection vJoy mode keeps open for
// telemetry. The backend only reports its readiness and closes it.
type Sim interface {
	Ready() bool
	Close() error
}

type axis struct {
	usage uint32
	max   int32
}

type Backend struct {
	sim  Sim
	axes map[control.AxisID]axis

	throttleMu  sync.Mutex // serializes throttle writes, resyncs and setting changes
	sync        throttleSync
	signed      bool
	invert      bool
	idlePoint   float64
	reverseHeld bool
}

// New acquires vJoy device 1, which must have every axis and button
// mHOTAS drives — a missing one is a startup error, not a silent gap.
func New(log *slog.Logger, sim Sim) (*Backend, error) {
	if err := vjoylink.Enabled(); err != nil {
		return nil, fmt.Errorf("vjoy: %w", err)
	}
	if err := vjoylink.Acquire(deviceID); err != nil {
		return nil, fmt.Errorf("vjoy: %w", err)
	}
	// A previous crashed session may have left a button held. Not ResetVJD:
	// that recenters every axis, which MSFS would apply as real input.
	vjoylink.ReleaseButtons(deviceID)

	b := &Backend{sim: sim, axes: map[control.AxisID]axis{}}
	var missing []string
	for id, usage := range axisUsage {
		if max, ok := vjoylink.AxisMax(deviceID, usage); ok {
			b.axes[id] = axis{usage: usage, max: max}
			log.Info("vjoy binding", "axis", axisLetter[usage], "control", id.String())
		} else {
			missing = append(missing, "axis "+axisLetter[usage])
		}
	}
	if n := vjoylink.ButtonCount(deviceID); n < int(control.ButtonThrottleReverseArm) {
		missing = append(missing, fmt.Sprintf("%d buttons (has %d)", control.ButtonThrottleReverseArm, n))
	}
	if len(missing) > 0 {
		vjoylink.Relinquish(deviceID)
		return nil, fmt.Errorf("vjoy device %d needs %s — enable them in vJoyConf, or run with --input=simconnect",
			deviceID, strings.Join(missing, ", "))
	}
	for id := control.ButtonGear; id <= control.ButtonThrottleReverseArm; id++ {
		log.Info("vjoy binding", "button", int(id), "control", id.String())
	}
	return b, nil
}

func (b *Backend) Name() string { return "vjoy" }

func (b *Backend) SetAxis(id control.AxisID, value float64) error {
	a := b.axes[id]
	if id != control.AxisThrottle {
		return vjoylink.SetAxis(deviceID, a.usage, toRaw(value, a.max))
	}
	b.throttleMu.Lock()
	defer b.throttleMu.Unlock()
	b.sync.wrote(time.Now())
	return b.writeThrottle(value)
}

// SyncThrottle implements control.ThrottleSyncer: moves the vJoy throttle
// to the sim's actual lever position when the sim changed it some other
// way — see throttleSync. MSFS receives the position it already has, so
// nothing moves in the sim.
func (b *Backend) SyncThrottle(sim float64, autothrottle bool) {
	b.throttleMu.Lock()
	defer b.throttleMu.Unlock()
	if b.sync.shouldSync(sim, autothrottle, time.Now()) {
		b.writeThrottle(sim)
	}
}

// writeThrottle writes the axis and, in the unsigned convention, the
// reverse-hold button with it — the axis alone is only a magnitude there,
// so a stale button would turn reverse into forward thrust or vice versa.
// Caller holds throttleMu.
func (b *Backend) writeThrottle(value float64) error {
	if err := b.holdReverse(!b.signed && value < 0); err != nil {
		return err
	}
	a := b.axes[control.AxisThrottle]
	return vjoylink.SetAxis(deviceID, a.usage, throttleToRaw(value, b.signed, b.idlePoint, b.invert, a.max))
}

// holdReverse presses the reverse button while the lever is in the reverse
// zone, edge-triggered so it isn't rewritten on every axis update. Same
// button the nudge control's REV hold uses, so one MSFS binding ("reverse
// thrust, hold") covers both. Caller holds throttleMu.
func (b *Backend) holdReverse(want bool) error {
	if b.reverseHeld == want {
		return nil
	}
	if err := vjoylink.SetButton(deviceID, uint8(control.ButtonThrottleReverseArm), want); err != nil {
		return err
	}
	b.reverseHeld = want
	return nil
}

// SetButton forwards press and release both: a vJoy button is held HID
// state, so whether it acts as a toggle, momentary or repeat is decided by
// the MSFS binding, not here.
func (b *Backend) SetButton(id control.ButtonID, pressed bool) error {
	return vjoylink.SetButton(deviceID, uint8(id), pressed)
}

// Ready reports whether the sim is connected: vJoy is always writable once
// acquired, but nothing reaches MSFS without it running.
func (b *Backend) Ready() bool { return b.sim.Ready() }

func (b *Backend) Close() error {
	vjoylink.ReleaseButtons(deviceID)
	vjoylink.Relinquish(deviceID)
	return b.sim.Close()
}

// SetSignedThrottle, SetSignedThrottleIdle and SetThrottleInvert implement
// control.SignedThrottleSetter.
func (b *Backend) SetSignedThrottle(signed bool) {
	b.throttleMu.Lock()
	defer b.throttleMu.Unlock()
	b.signed = signed
	if signed {
		b.holdReverse(false) // signed mode never touches the button again
	}
}

func (b *Backend) SetThrottleInvert(invert bool) {
	b.throttleMu.Lock()
	defer b.throttleMu.Unlock()
	b.invert = invert
}

func (b *Backend) SetSignedThrottleIdle(idle float64) {
	b.throttleMu.Lock()
	defer b.throttleMu.Unlock()
	b.idlePoint = idle
}
