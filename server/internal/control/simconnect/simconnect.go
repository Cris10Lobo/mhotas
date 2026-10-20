//go:build windows

// Package simconnect implements control.Output by driving MSFS2024
// through SimConnect, via server/internal/simlink. Validated against a
// live MSFS2024 session.
//
// SetAxis/SetButton contain no per-control branching: Backend wraps a
// control.MappedOutput built from this package's driver factory (see
// drivers.go) plus the effective control mapping (defaultMappings,
// overridden per-name by config.Config.Controls) — the same
// interfaces-and-configuration shape a future vjoy backend would use.
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

// New connects to MSFS2024 in the background (auto-reconnecting if the
// sim isn't running yet) and returns immediately with a usable Backend.
// SetAxis/SetButton calls made before MSFS2024 is reachable return a
// non-fatal error, which the transport layer already logs and continues
// past. controls overrides the backend's default control mapping by name
// (see defaultMappings) — nil/empty reproduces today's behavior exactly.
func New(ctx context.Context, log *slog.Logger, controls map[string]config.ControlMapping) (*Backend, error) {
	client := simlink.Connect(ctx, log)
	out, err := buildMappedOutput(client, log, controls)
	if err != nil {
		return nil, err
	}
	return &Backend{client: client, out: out}, nil
}

func (b *Backend) Name() string { return b.out.Name() }

// SimlinkClient exposes the underlying SimConnect connection so
// cmd/mhotas-server can share it with telemetry.Source — one connection
// serves both the write path (this Backend) and the read path (telemetry).
func (b *Backend) SimlinkClient() *simlink.Client { return b.client }

func (b *Backend) SetAxis(axis control.AxisID, value float64) error {
	return b.out.SetAxis(axis, value)
}

func (b *Backend) SetButton(id control.ButtonID, pressed bool) error {
	return b.out.SetButton(id, pressed)
}

// Ready reports whether the underlying SimConnect connection has completed
// its handshake with MSFS2024 — see simlink.Client.Ready.
func (b *Backend) Ready() bool {
	return b.out.Ready()
}

func (b *Backend) Close() error {
	return b.out.Close()
}

// SetSignedThrottle and SetSignedThrottleIdle implement
// control.SignedThrottleSetter.
func (b *Backend) SetSignedThrottle(signed bool) {
	b.out.SetSignedThrottle(signed)
}

func (b *Backend) SetThrottleInvert(invert bool) {
	b.out.SetThrottleInvert(invert)
}

func (b *Backend) SetSignedThrottleIdle(idle float64) {
	b.out.SetSignedThrottleIdle(idle)
}

// toAxisRange maps a calibrated -1..1 value to SimConnect's
// -simlink.AxisRange..simlink.AxisRange integer range. Correct for
// elevator/aileron/rudder: they're naturally centered controls, where 0
// already means neutral. Throttle uses toThrottleRaw instead — see
// throttle.go for why.
func toAxisRange(v float64) int32 {
	if v > 1 {
		v = 1
	}
	if v < -1 {
		v = -1
	}
	return int32(v * simlink.AxisRange)
}
