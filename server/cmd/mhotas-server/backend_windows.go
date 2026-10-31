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

// newBackend's telemetry starter is nil for the log backend. The vjoy and
// simconnect input modes never fall back to each other.
func newBackend(ctx context.Context, name, input string, log *slog.Logger, cfg config.Config) (control.Output, startTelemetryFunc, error) {
	switch {
	case name == "log":
		return logbackend.New(log), nil, nil
	case name == "simconnect" && input == "vjoy":
		client := simlink.Connect(ctx, log)
		vj, err := vjoy.New(log, client)
		if err != nil {
			client.Close()
			return nil, nil, err
		}
		return vj, telemetryStarter(ctx, client, vj, log), nil
	case name == "simconnect":
		sc, err := simconnect.New(ctx, log, cfg.Controls)
		if err != nil {
			return nil, nil, err
		}
		return sc, telemetryStarter(ctx, sc.SimlinkClient(), nil, log), nil
	default:
		return nil, nil, fmt.Errorf("unknown backend %q (want log or simconnect)", name)
	}
}

// syncer (vJoy only) is fed the sim's throttle because a vJoy axis holds its last value.
func telemetryStarter(ctx context.Context, client *simlink.Client, syncer control.ThrottleSyncer, log *slog.Logger) startTelemetryFunc {
	return func(sink telemetry.Sink) {
		src, err := telemetry.NewSource(client, syncer)
		if err != nil {
			log.Error("telemetry source init failed", "err", err)
			return
		}
		go func() {
			if err := src.Run(ctx, telemetry.LogChanges(sink, log)); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("telemetry source stopped", "err", err)
			}
		}()
	}
}
