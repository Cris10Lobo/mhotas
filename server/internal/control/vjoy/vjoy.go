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
	"sort"
	"sync"
	"sync/atomic"
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
	vjoylink.AxisX: "X", vjoylink.AxisY: "Y", vjoylink.AxisZ: "Z", vjoylink.AxisRZ: "RZ",
}

type axis struct {
	usage uint32
	max   int32
}

type Backend struct {
	axes        map[control.AxisID]axis // only axes the device actually has
	buttons     int
	signed      atomic.Bool
	invert      atomic.Bool
	reverseHeld atomic.Bool

	throttleMu sync.Mutex // serializes phone writes with telemetry resyncs
	sync       throttleSync
	idlePoint  float64 // guarded by throttleMu
}

// New acquires vJoy device 1. It fails only if vJoy itself is unusable
// (driver missing/disabled, device busy or not configured); controls the
// device merely lacks (an axis not ticked in vJoyConf, too few buttons) come
// back in unsupported so the caller can route just those elsewhere.
func New(log *slog.Logger) (b *Backend, unsupported []string, err error) {
	if err := vjoylink.Enabled(); err != nil {
		return nil, nil, fmt.Errorf("vjoy: %w", err)
	}
	if err := vjoylink.Acquire(deviceID); err != nil {
		return nil, nil, fmt.Errorf("vjoy: %w", err)
	}
	vjoylink.ReleaseButtons(deviceID) // a previous crashed session may have left one held

	b = &Backend{axes: map[control.AxisID]axis{}, buttons: vjoylink.ButtonCount(deviceID)}
	for id, usage := range axisUsage {
		if max, ok := vjoylink.AxisMax(deviceID, usage); ok {
			b.axes[id] = axis{usage: usage, max: max}
			log.Info("vjoy binding", "axis", axisLetter[usage], "control", id.String())
		} else {
			unsupported = append(unsupported, id.String())
		}
	}
	for id := control.ButtonGear; id <= control.ButtonThrottleReverseArm; id++ {
		if int(id) <= b.buttons {
			log.Info("vjoy binding", "button", int(id), "control", id.String())
		} else {
			unsupported = append(unsupported, id.String())
		}
	}
	sort.Strings(unsupported)
	return b, unsupported, nil
}

func (b *Backend) Name() string { return "vjoy" }

func (b *Backend) SetAxis(id control.AxisID, value float64) error {
	a, ok := b.axes[id]
	if !ok {
		return fmt.Errorf("vjoy: device has no axis for %s", id)
	}
	if id != control.AxisThrottle {
		return vjoylink.SetAxis(deviceID, a.usage, toRaw(value, a.max))
	}
	b.throttleMu.Lock()
	defer b.throttleMu.Unlock()
	b.sync.wrote(time.Now())
	signed := b.signed.Load()
	if !signed {
		b.holdReverse(value < 0)
	}
	return vjoylink.SetAxis(deviceID, a.usage, throttleToRaw(value, signed, b.idlePoint, b.invert.Load(), a.max))
}

// SyncThrottle implements control.ThrottleSyncer: moves the vJoy throttle
// to the sim's actual lever position when the sim changed it some other
// way — see throttleSync. value arrives already calibrated (see
// transport.Server.SyncThrottle), in the same units as SetAxis, so MSFS
// receives the position it already has and nothing moves in the sim. The
// reverse button isn't touched.
func (b *Backend) SyncThrottle(value float64, autothrottle bool) {
	a, ok := b.axes[control.AxisThrottle]
	if !ok {
		return
	}
	b.throttleMu.Lock()
	defer b.throttleMu.Unlock()
	if b.sync.shouldSync(value, autothrottle, time.Now()) {
		vjoylink.SetAxis(deviceID, a.usage, throttleToRaw(value, b.signed.Load(), b.idlePoint, b.invert.Load(), a.max))
	}
}

// holdReverse presses the reverse button while the lever is in the reverse
// zone (unsigned convention only), edge-triggered so it isn't rewritten on
// every axis update. Same button the nudge control's REV hold uses, so one
// MSFS binding ("reverse thrust, hold") covers both.
func (b *Backend) holdReverse(want bool) {
	if int(control.ButtonThrottleReverseArm) > b.buttons {
		return
	}
	if b.reverseHeld.Swap(want) != want {
		vjoylink.SetButton(deviceID, uint8(control.ButtonThrottleReverseArm), want)
	}
}

// SetButton forwards press and release both: a vJoy button is held HID
// state, so whether it acts as a toggle, momentary or repeat is decided by
// the MSFS binding, not here.
func (b *Backend) SetButton(id control.ButtonID, pressed bool) error {
	if int(id) > b.buttons {
		return fmt.Errorf("vjoy: device has no button %d (%s)", id, id)
	}
	return vjoylink.SetButton(deviceID, uint8(id), pressed)
}

func (b *Backend) Ready() bool { return true }

func (b *Backend) Close() error {
	vjoylink.ReleaseButtons(deviceID)
	vjoylink.Relinquish(deviceID)
	return nil
}

// SetSignedThrottle and SetSignedThrottleIdle implement
// control.SignedThrottleSetter.
func (b *Backend) SetSignedThrottle(signed bool) {
	b.signed.Store(signed)
	if signed {
		b.holdReverse(false)
	}
}

func (b *Backend) SetThrottleInvert(invert bool) { b.invert.Store(invert) }

func (b *Backend) SetSignedThrottleIdle(idle float64) {
	b.throttleMu.Lock()
	b.idlePoint = idle
	b.throttleMu.Unlock()
}
