package control

import "fmt"

// AxisDriver writes one axis value to the simulator.
type AxisDriver interface {
	SetAxis(value float64) error
}

type ButtonDriver interface {
	SetButton(pressed bool) error
}

// MappedOutput is an Output that delegates to the driver mapped to each control.
type MappedOutput struct {
	name    string
	axes    map[AxisID]AxisDriver
	buttons map[ButtonID]ButtonDriver
	ready   func() bool
	close   func() error
}

// NewMappedOutput: ready and close may be nil.
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

// The SignedThrottleSetter methods forward to the throttle driver and are
// no-ops if it doesn't implement the interface.
func (m *MappedOutput) SetSignedThrottle(signed bool) {
	if d, ok := m.axes[AxisThrottle].(SignedThrottleSetter); ok {
		d.SetSignedThrottle(signed)
	}
}

func (m *MappedOutput) SetThrottleInvert(invert bool) {
	if d, ok := m.axes[AxisThrottle].(SignedThrottleSetter); ok {
		d.SetThrottleInvert(invert)
	}
}

func (m *MappedOutput) SetSignedThrottleIdle(idle float64) {
	if d, ok := m.axes[AxisThrottle].(SignedThrottleSetter); ok {
		d.SetSignedThrottleIdle(idle)
	}
}
