// Command mhotas-server is the PC-side server: it listens for phone
// control packets over UDP and drives a control.Output backend
// (log/simconnect/vjoy).
//
// newBackend is implemented per-OS (backend_windows.go / backend_other.go)
// since the simconnect/vjoy backends are Windows-only packages — this file
// must stay buildable on every platform.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"mhotas/internal/config"
	"mhotas/internal/transport"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mhotas-server:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		backendName = flag.String("backend", "log", "control output backend: log, simconnect, vjoy")
		configPath  = flag.String("config", "", "path to a JSON config file (optional; defaults are used if omitted)")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	backend, err := newBackend(ctx, *backendName, log)
	if err != nil {
		return err
	}
	defer backend.Close()

	srv, err := transport.New(cfg, backend, log)
	if err != nil {
		return fmt.Errorf("starting transport: %w", err)
	}
	startTelemetry(ctx, backend, srv, log)

	log.Info("mhotas-server listening", "addr", srv.Addr(), "backend", backend.Name())

	return srv.Run(ctx)
}
