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
	"mhotas/internal/control/vjoy"
	"mhotas/internal/simlink"
	"mhotas/internal/telemetry"
)

// newBackend also returns the telemetry starter for the SimConnect
// connection it opened (nil for log), so telemetry is wired to the
// connection itself rather than to whatever Output wraps it.
//
// The simconnect backend always connects to SimConnect — telemetry needs it
// in either input mode. With input "vjoy" the writes go through vJoy
// instead, with SimConnect still taking any control vJoy can't drive, or all
// of them if vJoy itself is unavailable.
func newBackend(ctx context.Context, name, input string, log *slog.Logger, cfg config.Config) (control.Output, startTelemetryFunc, error) {
	switch name {
	case "log":
		return logbackend.New(log), nil, nil
	case "simconnect":
		sc, err := simconnect.New(ctx, log, cfg.Controls)
		if err != nil {
			return nil, nil, err
		}
		start := telemetryStarter(ctx, sc.SimlinkClient(), log)
		if input != "vjoy" {
			return sc, start, nil
		}
		vj, unsupported, err := vjoy.New(log)
		if err != nil {
			log.Warn("vJoy unavailable, using SimConnect for all input", "err", err)
			return sc, start, nil
		}
		if len(unsupported) > 0 {
			log.Warn("vJoy device lacks these controls, using SimConnect for them", "controls", unsupported)
		}
		r, err := control.NewRouter(vj, sc, append(unsupported, cfg.SimConnectControls...))
		if err != nil {
			vj.Close()
			sc.Close()
			return nil, nil, fmt.Errorf("simconnect_controls: %w", err)
		}
		return r, start, nil
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
