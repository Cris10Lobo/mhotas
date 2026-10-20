package control

import (
	"errors"
	"fmt"
)

// Router sends each control to primary, except the named ones, which go to
// fallback. It's how vJoy input mode keeps a control working through
// SimConnect when the vJoy device can't drive it (or the user chose
// SimConnect for it in config).
type Router struct {
	primary, fallback Output
	axes              map[AxisID]bool
	buttons           map[ButtonID]bool
}

// NewRouter fails on a name that isn't a known axis or button, so a typo in
// config is caught at startup instead of silently routing nothing.
func NewRouter(primary, fallback Output, toFallback []string) (*Router, error) {
	r := &Router{primary: primary, fallback: fallback, axes: map[AxisID]bool{}, buttons: map[ButtonID]bool{}}
	for _, name := range toFallback {
		if id, ok := AxisByName(name); ok {
			r.axes[id] = true
		} else if id, ok := ButtonByName(name); ok {
			r.buttons[id] = true
		} else {
			return nil, fmt.Errorf("unknown control %q (want an axis or button name, e.g. \"rudder\", \"gear\")", name)
		}
	}
	return r, nil
}

func (r *Router) Name() string { return r.primary.Name() + "+" + r.fallback.Name() }

func (r *Router) SetAxis(id AxisID, value float64) error {
	if r.axes[id] {
		return r.fallback.SetAxis(id, value)
	}
	return r.primary.SetAxis(id, value)
}

func (r *Router) SetButton(id ButtonID, pressed bool) error {
	if r.buttons[id] {
		return r.fallback.SetButton(id, pressed)
	}
	return r.primary.SetButton(id, pressed)
}

func (r *Router) Ready() bool { return r.primary.Ready() && r.fallback.Ready() }

func (r *Router) Close() error { return errors.Join(r.primary.Close(), r.fallback.Close()) }

// Route reports which backend a control (by config name) goes to — for the
// TUI's route table.
func (r *Router) Route(name string) string {
	if id, ok := AxisByName(name); ok && r.axes[id] {
		return r.fallback.Name()
	}
	if id, ok := ButtonByName(name); ok && r.buttons[id] {
		return r.fallback.Name()
	}
	return r.primary.Name()
}

// SetSignedThrottle/SetSignedThrottleIdle forward to whichever backend
// drives the throttle.
func (r *Router) SetSignedThrottle(signed bool) {
	if s, ok := r.throttle().(SignedThrottleSetter); ok {
		s.SetSignedThrottle(signed)
	}
}

func (r *Router) SetSignedThrottleIdle(idle float64) {
	if s, ok := r.throttle().(SignedThrottleSetter); ok {
		s.SetSignedThrottleIdle(idle)
	}
}

func (r *Router) SyncThrottle(sim float64, autothrottle bool) {
	if s, ok := r.throttle().(ThrottleSyncer); ok {
		s.SyncThrottle(sim, autothrottle)
	}
}

func (r *Router) throttle() Output {
	if r.axes[AxisThrottle] {
		return r.fallback
	}
	return r.primary
}
