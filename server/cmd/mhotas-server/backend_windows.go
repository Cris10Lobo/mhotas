//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"mhotas/internal/control"
	"mhotas/internal/control/logbackend"
	"mhotas/internal/control/simconnect"
	"mhotas/internal/control/vjoy"
	"mhotas/internal/telemetry"
)

func newBackend(ctx context.Context, name string, log *slog.Logger) (control.Output, error) {
	switch name {
	case "log":
		return logbackend.New(log), nil
	case "simconnect":
		return simconnect.New(ctx, log)
	case "vjoy":
		return vjoy.New()
	default:
		return nil, fmt.Errorf("unknown backend %q (want log, simconnect, or vjoy)", name)
	}
}

// startTelemetry starts pushing live flap/gear state to sink in the
// background, if backend supports it (only simconnect does — one
// SimConnect connection serves both the write and read paths).
func startTelemetry(ctx context.Context, backend control.Output, sink telemetry.Sink, log *slog.Logger) {
	sc, ok := backend.(*simconnect.Backend)
	if !ok {
		return
	}
	src, err := telemetry.NewSource(sc.SimlinkClient())
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
