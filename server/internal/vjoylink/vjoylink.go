//go:build windows

// Package vjoylink is a minimal binding to vJoyInterface.dll, the vJoy
// virtual joystick driver's user-mode API. Only what control/vjoy needs.
package vjoylink

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// HID usage IDs vJoyInterface.dll addresses axes by.
const (
	AxisX  uint32 = 0x30
	AxisY  uint32 = 0x31
	AxisZ  uint32 = 0x32
	AxisRZ uint32 = 0x35
)

// GetVJDStatus results.
const (
	statOwn  = 0
	statFree = 1
)

// procs is filled once at init and only read afterwards, so concurrent
// callers (transport's writes, telemetry's throttle resync) need no lock.
var procs, loadErr = load()

// load tries the normal search path (exe dir first) and then vJoy's own
// install dir, which the installer doesn't add to PATH.
func load() (map[string]*syscall.Proc, error) {
	paths := []string{
		"vJoyInterface.dll",
		filepath.Join(os.Getenv("ProgramFiles"), "vJoy", "x64", "vJoyInterface.dll"),
	}
	var dll *syscall.DLL
	for _, p := range paths {
		if d, err := syscall.LoadDLL(p); err == nil {
			dll = d
			break
		}
	}
	if dll == nil {
		return nil, fmt.Errorf("vJoyInterface.dll not found (looked next to the exe and in %s)", paths[1])
	}
	procs := map[string]*syscall.Proc{}
	for _, name := range []string{
		"vJoyEnabled", "GetVJDStatus", "AcquireVJD", "RelinquishVJD", "ResetVJD",
		"GetVJDAxisMax", "GetVJDButtonNumber", "SetAxis", "SetBtn",
	} {
		p, err := dll.FindProc(name)
		if err != nil {
			return nil, fmt.Errorf("vJoyInterface.dll: %w", err)
		}
		procs[name] = p
	}
	return procs, nil
}

//go:uintptrescapes
func call(name string, args ...uintptr) (uintptr, error) {
	if loadErr != nil {
		return 0, loadErr
	}
	r, _, _ := procs[name].Call(args...)
	return r, nil
}

// isTrue reads a Win32 BOOL return. Only the low 32 bits are defined; the
// upper half of the register can hold garbage on x64.
func isTrue(r uintptr) bool { return uint32(r) != 0 }

// Enabled reports whether the vJoy driver is installed and enabled.
func Enabled() error {
	r, err := call("vJoyEnabled")
	if err != nil {
		return err
	}
	if !isTrue(r) {
		return fmt.Errorf("vJoy driver not enabled — install vJoy and enable a device in vJoyConf")
	}
	return nil
}

// Acquire takes ownership of device id, failing if another app holds it or
// it isn't configured.
func Acquire(id uint32) error {
	r, err := call("GetVJDStatus", uintptr(id))
	if err != nil {
		return err
	}
	switch r {
	case statOwn:
		return nil
	case statFree:
	default:
		return fmt.Errorf("vJoy device %d unavailable (status %d: busy or not configured in vJoyConf)", id, r)
	}
	if r, err = call("AcquireVJD", uintptr(id)); err != nil {
		return err
	}
	if !isTrue(r) {
		return fmt.Errorf("vJoy device %d: acquire failed", id)
	}
	return nil
}

func Relinquish(id uint32) { call("RelinquishVJD", uintptr(id)) }

func Reset(id uint32) { call("ResetVJD", uintptr(id)) }

// AxisMax returns the axis's maximum raw value (its minimum is 0), and
// false if the axis isn't enabled on the device. Used as the existence
// check too: GetVJDAxisExist has been unreliable in practice.
func AxisMax(id, axis uint32) (int32, bool) {
	var max int32
	r, err := call("GetVJDAxisMax", uintptr(id), uintptr(axis), uintptr(unsafe.Pointer(&max)))
	if err != nil || !isTrue(r) || max <= 0 {
		return 0, false
	}
	return max, true
}

// ButtonCount returns how many buttons device id has enabled.
func ButtonCount(id uint32) int {
	r, err := call("GetVJDButtonNumber", uintptr(id))
	if err != nil {
		return 0
	}
	return int(int32(r))
}

func SetAxis(id, axis uint32, value int32) error {
	r, err := call("SetAxis", uintptr(uint32(value)), uintptr(id), uintptr(axis))
	if err != nil {
		return err
	}
	if !isTrue(r) {
		return fmt.Errorf("vJoy SetAxis(device %d, axis 0x%x) failed", id, axis)
	}
	return nil
}

func SetButton(id uint32, button uint8, pressed bool) error {
	var v uintptr
	if pressed {
		v = 1
	}
	r, err := call("SetBtn", v, uintptr(id), uintptr(button))
	if err != nil {
		return err
	}
	if !isTrue(r) {
		return fmt.Errorf("vJoy SetBtn(device %d, button %d) failed", id, button)
	}
	return nil
}
