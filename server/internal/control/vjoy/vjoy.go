//go:build windows

// Package vjoy implements control.Output on a vJoy virtual joystick. The
// mapping is fixed (meaning is set by the MSFS binding); button N is
// control.ButtonID N.
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

// ponytail: single hardcoded device; one phone, one PC.
const deviceID = 1

var axisUsage = map[control.AxisID]uint32{
	control.AxisPitch:      vjoylink.AxisY,
	control.AxisRoll:       vjoylink.AxisX,
	control.AxisThrottle:   vjoylink.AxisZ,
	control.AxisRudder:     vjoylink.AxisRZ,
	control.AxisSpeedbrake: vjoylink.AxisRX,
}

var axisLetter = map[uint32]string{
	vjoylink.AxisX: "X", vjoylink.AxisY: "Y", vjoylink.AxisZ: "Z", vjoylink.AxisRX: "Rx", vjoylink.AxisRZ: "Rz",
}

// Sim is the SimConnect connection kept open for telemetry; Backend owns closing it.
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

	speedbrakeRaw int32 // last written; -1 before the first write

	throttleMu  sync.Mutex // serializes throttle writes, resyncs and setting changes
	sync        throttleSync
	signed      bool
	invert      bool
	idlePoint   float64
	reverseHeld bool
}

// New acquires vJoy device 1, failing if any axis or button we drive is missing.
func New(log *slog.Logger, sim Sim) (*Backend, error) {
	if err := vjoylink.Enabled(); err != nil {
		return nil, fmt.Errorf("vjoy: %w", err)
	}
	if err := vjoylink.Acquire(deviceID); err != nil {
		return nil, fmt.Errorf("vjoy: %w", err)
	}
	// A crashed session may have left a button held.
	vjoylink.ReleaseButtons(deviceID)

	b := &Backend{sim: sim, axes: map[control.AxisID]axis{}, speedbrakeRaw: -1}
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
		return nil, fmt.Errorf("vjoy device %d needs %s: enable them in vJoyConf, or run with --input=simconnect",
			deviceID, strings.Join(missing, ", "))
	}
	for id := control.ButtonGear; id <= control.ButtonThrottleReverseArm; id++ {
		if _, used := control.ButtonByName(id.String()); !used {
			continue
		}
		log.Info("vjoy binding", "button", int(id), "control", id.String())
	}
	return b, nil
}

func (b *Backend) Name() string { return "vjoy" }

func (b *Backend) SetAxis(id control.AxisID, value float64) error {
	a := b.axes[id]
	switch id {
	case control.AxisThrottle:
		b.throttleMu.Lock()
		defer b.throttleMu.Unlock()
		b.sync.wrote(time.Now())
		return b.writeThrottle(value)
	case control.AxisSpeedbrake:
		// Retracted = axis minimum, like a hardware speedbrake lever.
		raw := toRaw(value*2-1, a.max)
		// MSFS only acts on an axis change, so re-sending the value the axis
		// already holds (first command after a reconnect, after the sim moved
		// the handle itself) did nothing; step off it first, for long enough
		// that MSFS's once-a-frame poll sees it. -1: unknown since startup.
		if raw == b.speedbrakeRaw || b.speedbrakeRaw == -1 {
			if err := vjoylink.SetAxis(deviceID, a.usage, raw^1); err != nil {
				return err
			}
			time.Sleep(50 * time.Millisecond)
		}
		b.speedbrakeRaw = raw
		return vjoylink.SetAxis(deviceID, a.usage, raw)
	default:
		return vjoylink.SetAxis(deviceID, a.usage, toRaw(value, a.max))
	}
}

// SyncThrottle implements control.ThrottleSyncer.
func (b *Backend) SyncThrottle(sim float64, autothrottle bool) {
	b.throttleMu.Lock()
	defer b.throttleMu.Unlock()
	if b.sync.shouldSync(sim, autothrottle, time.Now()) {
		b.writeThrottle(sim)
	}
}

// writeThrottle also sets the reverse-hold button in the unsigned
// convention, where the axis is only a magnitude. Caller holds throttleMu.
func (b *Backend) writeThrottle(value float64) error {
	if err := b.holdReverse(!b.signed && value < 0); err != nil {
		return err
	}
	a := b.axes[control.AxisThrottle]
	return vjoylink.SetAxis(deviceID, a.usage, throttleToRaw(value, b.signed, b.idlePoint, b.invert, a.max))
}

// holdReverse is edge-triggered. It is the same button the nudge control's
// REV hold uses, so one MSFS binding covers both. Caller holds throttleMu.
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

func (b *Backend) SetButton(id control.ButtonID, pressed bool) error {
	return vjoylink.SetButton(deviceID, uint8(id), pressed)
}

// Ready reports sim connectivity; vJoy itself is always writable once acquired.
func (b *Backend) Ready() bool { return b.sim.Ready() }

func (b *Backend) Close() error {
	vjoylink.ReleaseButtons(deviceID)
	vjoylink.Relinquish(deviceID)
	return b.sim.Close()
}

func (b *Backend) SetSignedThrottle(signed bool) {
	b.throttleMu.Lock()
	defer b.throttleMu.Unlock()
	b.signed = signed
	if signed {
		b.holdReverse(false)
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
