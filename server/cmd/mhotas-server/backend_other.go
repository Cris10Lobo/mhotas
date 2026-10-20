//go:build !windows

package main

import (
	"context"
	"fmt"
	"log/slog"

	"mhotas/internal/config"
	"mhotas/internal/control"
	"mhotas/internal/control/logbackend"
)

func newBackend(_ context.Context, name, _ string, log *slog.Logger, _ config.Config) (control.Output, startTelemetryFunc, error) {
	switch name {
	case "log":
		return logbackend.New(log), nil, nil
	case "simconnect":
		return nil, nil, fmt.Errorf("%q backend requires building/running on Windows (GOOS=windows)", name)
	default:
		return nil, nil, fmt.Errorf("unknown backend %q (want log or simconnect)", name)
	}
}
