// Package logbackend is a control.Output that only logs, for --backend=log.
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

func (b *Backend) Ready() bool { return true }

func (b *Backend) Close() error { return nil }
