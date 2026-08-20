// Package logbackend implements control.Output by logging every call
// instead of driving a simulator. It lets the transport, protocol, and
// calibration layers be developed and tested without MSFS running at all,
// and doubles as a permanent --backend=log debug flag.
package logbackend

import (
	"log/slog"

	"mhotas/internal/control"
)

type Backend struct {
	log *slog.Logger
}

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

// Ready is always true — there's no external connection to be ready for,
// logging always "works".
func (b *Backend) Ready() bool { return true }

func (b *Backend) Close() error { return nil }
