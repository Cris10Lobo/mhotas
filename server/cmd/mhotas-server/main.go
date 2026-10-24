// Command mhotas-server is the PC-side server for the mHOTAS phone app.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"mhotas/internal/config"
	"mhotas/internal/telemetry"
	"mhotas/internal/transport"
	"mhotas/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mhotas-server:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		backendName = flag.String("backend", "log", "control output backend: log, simconnect")
		configPath  = flag.String("config", "", "path to a JSON config file (optional; defaults are used if omitted)")
		inputMode   = flag.String("input", "", "simconnect backend's input path: vjoy (default; SimConnect only reads telemetry) or simconnect")
		uiMode      = flag.String("ui", "auto", "display mode: auto, tui, plain (auto = tui when stdout is a terminal)")
	)
	flag.Parse()

	if *uiMode != "auto" && *uiMode != "tui" && *uiMode != "plain" {
		return fmt.Errorf("unknown -ui %q (want auto, tui, or plain)", *uiMode)
	}
	useTUI := *uiMode == "tui" || (*uiMode == "auto" && isTerminal(os.Stdout))

	// The TUI owns the terminal, so logs must not go to stdout.
	var tail *tui.LogTail
	var log *slog.Logger
	if useTUI {
		tail = tui.NewLogTail(500)
		log = slog.New(tail)
	} else {
		log = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}

	input := *inputMode
	if input == "" && useTUI && *backendName == "simconnect" {
		picked, err := tui.PickInput()
		if errors.Is(err, tui.ErrCancelled) {
			return nil
		}
		if err != nil {
			return err
		}
		input = picked
	}
	if input == "" {
		input = "vjoy"
	}
	if input != "vjoy" && input != "simconnect" {
		return fmt.Errorf("unknown -input %q (want vjoy or simconnect)", input)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	backend, startTelemetry, err := newBackend(ctx, *backendName, input, log, cfg)
	if err != nil {
		return err
	}
	defer backend.Close()

	srv, err := transport.New(cfg, backend, log, *configPath)
	if err != nil {
		return fmt.Errorf("starting transport: %w", err)
	}
	if startTelemetry != nil {
		startTelemetry(srv)
	}

	if !useTUI {
		log.Info("mhotas-server listening", "addr", srv.Addr(), "backend", backend.Name())
		return srv.Run(ctx)
	}

	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.Run(ctx) }()

	p := tea.NewProgram(tui.New(srv, backend, tail), tea.WithAltScreen())
	// A signal cancels ctx without going through the TUI's key handling.
	go func() {
		<-ctx.Done()
		p.Quit()
	}()

	_, runErr := p.Run()
	stop() // quitting the TUI must also stop the server
	if srvErr := <-serverErr; srvErr != nil && runErr == nil {
		runErr = srvErr
	}
	return runErr
}

// startTelemetryFunc is nil for backends without a SimConnect read path.
type startTelemetryFunc func(sink telemetry.Sink)

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
