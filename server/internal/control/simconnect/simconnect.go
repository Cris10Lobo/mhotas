//go:build windows

// Package simconnect implements control.Output by driving MSFS2024 through SimConnect.
package simconnect

import (
	"context"
	"log/slog"

	"mhotas/internal/config"
	"mhotas/internal/control"
	"mhotas/internal/simlink"
)

type Backend struct {
	client *simlink.Client
	out    *control.MappedOutput
}

// New connects in the background and returns immediately; writes before MSFS2024
// is reachable return non-fatal errors. controls overrides defaultMappings by name.
func New(ctx context.Context, log *slog.Logger, controls map[string]config.ControlMapping) (*Backend, error) {
	client := simlink.Connect(ctx, log)
	out, err := buildMappedOutput(client, log, controls)
	if err != nil {
		return nil, err
	}
	return &Backend{client: client, out: out}, nil
}

func (b *Backend) Name() string { return b.out.Name() }

// SimlinkClient returns the connection, shared with telemetry.Source.
func (b *Backend) SimlinkClient() *simlink.Client { return b.client }

func (b *Backend) SetAxis(axis control.AxisID, value float64) error {
	return b.out.SetAxis(axis, value)
}

func (b *Backend) SetButton(id control.ButtonID, pressed bool) error {
	return b.out.SetButton(id, pressed)
}

func (b *Backend) Ready() bool {
	return b.out.Ready()
}

func (b *Backend) Close() error {
	return b.out.Close()
}

func (b *Backend) SetSignedThrottle(signed bool) {
	b.out.SetSignedThrottle(signed)
}

func (b *Backend) SetThrottleInvert(invert bool) {
	b.out.SetThrottleInvert(invert)
}

func (b *Backend) SetSignedThrottleIdle(idle float64) {
	b.out.SetSignedThrottleIdle(idle)
}

// toAxisRange maps -1..1 to +-simlink.AxisRange. Only for centered axes;
// throttle uses toSignedThrottleRaw.
func toAxisRange(v float64) int32 {
	return int32(max(-1, min(1, v)) * simlink.AxisRange)
}
