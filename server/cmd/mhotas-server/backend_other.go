//go:build !windows

package main

import (
	"context"
	"fmt"
	"log/slog"

	"mhotas/internal/control"
	"mhotas/internal/control/logbackend"
	"mhotas/internal/telemetry"
)

func newBackend(ctx context.Context, name string, log *slog.Logger) (control.Output, error) {
	switch name {
	case "log":
		return logbackend.New(log), nil
	case "simconnect", "vjoy":
		return nil, fmt.Errorf("%q backend requires building/running on Windows (GOOS=windows)", name)
	default:
		return nil, fmt.Errorf("unknown backend %q (want log, simconnect, or vjoy)", name)
	}
}

// startTelemetry is a no-op here: the only telemetry source so far
// (simconnect) is Windows-only — see backend_windows.go.
func startTelemetry(_ context.Context, _ control.Output, _ telemetry.Sink, _ *slog.Logger) {}
