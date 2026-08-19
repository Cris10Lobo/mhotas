//go:build windows

// Package vjoy will implement control.Output via the vJoy virtual joystick
// driver — a later-phase addition for broader sim compatibility beyond
// MSFS2024, and a fallback if direct SimConnect axis control
// (server/internal/control/simconnect) proves unreliable for some
// aircraft. Stubbed early, before it's needed, specifically so
// control.Output has a second real implementation validating the
// interface shape isn't accidentally SimConnect-specific.
package vjoy

import (
	"errors"

	"mhotas/internal/control"
)

var errNotImplemented = errors.New("vjoy backend is not implemented yet")

type Backend struct{}

func New() (*Backend, error) {
	return &Backend{}, nil
}

func (b *Backend) Name() string { return "vjoy" }

func (b *Backend) SetAxis(axis control.AxisID, value float64) error {
	return errNotImplemented
}

func (b *Backend) SetButton(id control.ButtonID, pressed bool) error {
	return errNotImplemented
}

func (b *Backend) Close() error { return nil }
