package control

import "fmt"

// AxisDriver is the mechanism that actually writes one axis value to the
// simulator — a K:EVENT transmit, an Input Event write, a future vJoy axis
// set. MappedOutput holds one per configured AxisID; nothing above this
// interface knows or cares which.
type AxisDriver interface {
	SetAxis(value float64) error
}

// ButtonDriver is AxisDriver's button-shaped counterpart.
type ButtonDriver interface {
	SetButton(pressed bool) error
}

// MappedOutput is a backend-agnostic Output: it does nothing but look up the
// driver mapped to an AxisID/ButtonID and delegate. A backend builds one from
// its own driver factory plus a config-declared mapping (see
// control/simconnect's driver factory for the reference implementation) — no
// backend's SetAxis/SetButton branches per-control internally anymore; that
// logic exists exactly once, here.
type MappedOutput struct {
	name    string
	axes    map[AxisID]AxisDriver
	buttons map[ButtonID]ButtonDriver
	ready   func() bool
	close   func() error
}

// NewMappedOutput builds a MappedOutput. ready/close may be nil (Ready
// always true, Close a no-op) for a backend with nothing to track.
func NewMappedOutput(name string, axes map[AxisID]AxisDriver, buttons map[ButtonID]ButtonDriver, ready func() bool, close func() error) *MappedOutput {
	return &MappedOutput{name: name, axes: axes, buttons: buttons, ready: ready, close: close}
}

func (m *MappedOutput) Name() string { return m.name }

func (m *MappedOutput) SetAxis(id AxisID, value float64) error {
	d, ok := m.axes[id]
	if !ok {
		return fmt.Errorf("%s: no driver mapped for axis %v", m.name, id)
	}
	return d.SetAxis(value)
}

func (m *MappedOutput) SetButton(id ButtonID, pressed bool) error {
	d, ok := m.buttons[id]
	if !ok {
		return fmt.Errorf("%s: no driver mapped for button %v", m.name, id)
	}
	return d.SetButton(pressed)
}

func (m *MappedOutput) Ready() bool {
	if m.ready == nil {
		return true
	}
	return m.ready()
}

func (m *MappedOutput) Close() error {
	if m.close == nil {
		return nil
	}
	return m.close()
}

// SetSignedThrottle and SetSignedThrottleIdle implement SignedThrottleSetter
// by forwarding to the throttle axis's own driver, if that driver supports
// it (throttleEx1Driver does; nothing else needs to) — a no-op otherwise,
// e.g. no throttle mapped, or a driver kind with no such concept.
func (m *MappedOutput) SetSignedThrottle(signed bool) {
	if d, ok := m.axes[AxisThrottle].(SignedThrottleSetter); ok {
		d.SetSignedThrottle(signed)
	}
}

func (m *MappedOutput) SetSignedThrottleIdle(idle float64) {
	if d, ok := m.axes[AxisThrottle].(SignedThrottleSetter); ok {
		d.SetSignedThrottleIdle(idle)
	}
}
