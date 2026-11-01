//go:build windows

// Command simprobe prints simvars and L:vars from the running sim, so the
// sim's side of a test can be checked from a shell:
//
//	simprobe "SPOILERS HANDLE POSITION,percent" L:A32NX_SPOILERS_HANDLE_POSITION
//
// A var without ",unit" is read as "number".
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"mhotas/internal/simlink"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "simprobe:", err)
		os.Exit(1)
	}
}

func run() error {
	watch := flag.Duration("watch", 0, "re-read at this interval until killed (0 = read once)")
	flag.Parse()
	if flag.NArg() == 0 {
		return fmt.Errorf("usage: simprobe [-watch 500ms] VAR[,unit]...")
	}
	var names, units []string
	for _, arg := range flag.Args() {
		name, unit, ok := strings.Cut(arg, ",")
		if !ok {
			unit = "number"
		}
		names, units = append(names, name), append(units, unit)
	}

	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := simlink.Connect(ctx, quiet)
	defer client.Close()

	deadline := time.Now().Add(15 * time.Second)
	for !client.Ready() {
		if time.Now().After(deadline) {
			return fmt.Errorf("no SimConnect connection after 15s: is MSFS running?")
		}
		time.Sleep(100 * time.Millisecond)
	}

	for {
		readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		values, err := client.ReadOnce(readCtx, names, units)
		cancel()
		if err != nil {
			return err
		}
		parts := make([]string, len(names))
		for i, name := range names {
			parts[i] = fmt.Sprintf("%s=%g", name, values[i])
		}
		if *watch == 0 {
			fmt.Println(strings.Join(parts, " "))
			return nil
		}
		fmt.Println(time.Now().Format("15:04:05.000"), strings.Join(parts, " "))
		time.Sleep(*watch)
	}
}
