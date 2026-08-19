// Package logbackend implements control.Output by logging every call
// instead of driving a simulator. It lets the transport, protocol, and
// calibration layers be developed and tested without MSFS running at all,
// and doubles as a permanent --backend=log debug flag.
package logbackend

import (
	"log/slog"

	"mhotas/internal/control"
)

// Backend is a control.Output that logs instead of writing to a simulator.
type Backend struct {
	log *slog.Logger
}

// New returns a Backend that logs to log.
func New(log *slog.Logger) *Backend {
	return &Backend{log: log}
}

func (b *Backend) Name() string { return "log" }

func (b *Backend) SetAxis(axis control.AxisID, value float64) error {
	b.log.Info("axis", "axis", axis, "value", value)
	return nil
}

func (b *Backend) SetButton(id control.ButtonID, pressed bool) error {
	b.log.Info("button", "id", id, "pressed", pressed)
	return nil
}

func (b *Backend) Close() error { return nil }
