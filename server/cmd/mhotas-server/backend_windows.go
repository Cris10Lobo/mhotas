//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"mhotas/internal/config"
	"mhotas/internal/control"
	"mhotas/internal/control/logbackend"
	"mhotas/internal/control/simconnect"
	"mhotas/internal/simlink"
	"mhotas/internal/telemetry"
)

// newBackend also returns the telemetry starter for the SimConnect
// connection it opened (nil for log), so telemetry is wired to the
// connection itself rather than to whatever Output wraps it.
func newBackend(ctx context.Context, name string, log *slog.Logger, controls map[string]config.ControlMapping) (control.Output, startTelemetryFunc, error) {
	switch name {
	case "log":
		return logbackend.New(log), nil, nil
	case "simconnect":
		sc, err := simconnect.New(ctx, log, controls)
		if err != nil {
			return nil, nil, err
		}
		return sc, telemetryStarter(ctx, sc.SimlinkClient(), log), nil
	default:
		return nil, nil, fmt.Errorf("unknown backend %q (want log or simconnect)", name)
	}
}

func telemetryStarter(ctx context.Context, client *simlink.Client, log *slog.Logger) startTelemetryFunc {
	return func(sink telemetry.Sink) {
		src, err := telemetry.NewSource(client)
		if err != nil {
			log.Error("telemetry source init failed", "err", err)
			return
		}
		go func() {
			if err := src.Run(ctx, sink); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("telemetry source stopped", "err", err)
			}
		}()
	}
}
