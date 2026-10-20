// Command mhotas-server is the PC-side server: it listens for phone
// control packets over UDP and drives a control.Output backend
// (log/simconnect).
//
// newBackend is implemented per-OS (backend_windows.go / backend_other.go)
// since the simconnect backend is Windows-only packages — this file
// must stay buildable on every platform.
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

	useTUI := *uiMode == "tui" || (*uiMode == "auto" && isTerminal(os.Stdout))
	if *uiMode != "auto" && *uiMode != "tui" && *uiMode != "plain" {
		return fmt.Errorf("unknown -ui %q (want auto, tui, or plain)", *uiMode)
	}

	// TUI mode owns the terminal (alternate screen) — logging straight to
	// stdout would corrupt it, so route through a tail the dashboard polls
	// instead of a stream, same reasoning as the app suppressing a moved
	// widget's own gestures during layout-edit mode: only one thing gets to
	// own the display surface at a time.
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
	// A signal (Ctrl+C at the OS level, not the TUI's own 'q'/ctrl+c key
	// handling) cancels ctx directly — make sure that also tears down the
	// TUI instead of leaving it on screen after the server's already gone.
	go func() {
		<-ctx.Done()
		p.Quit()
	}()

	_, runErr := p.Run()
	stop() // 'q' in the TUI only quits the TUI; cancel ctx so srv.Run exits too
	if srvErr := <-serverErr; srvErr != nil && runErr == nil {
		runErr = srvErr
	}
	return runErr
}

// startTelemetryFunc starts streaming live sim state to sink in the
// background — only backends with a SimConnect read path return one.
type startTelemetryFunc func(sink telemetry.Sink)

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
