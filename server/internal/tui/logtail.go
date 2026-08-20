// Package tui is the server's terminal dashboard: a QR code to connect the
// phone plus live sim/device connection state in one screen, replacing a
// scrolling log as the default way to see whether things are working. Pure
// display — it reads state from transport.Server/control.Output, it never
// drives them.
package tui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// LogTail is an slog.Handler that keeps the last N formatted lines in
// memory instead of writing them to a stream — the dashboard's log pane
// polls Lines() on each render tick, since printing straight to stdout
// would corrupt bubbletea's alternate-screen rendering.
type LogTail struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func NewLogTail(max int) *LogTail {
	return &LogTail{max: max}
}

func (t *LogTail) Enabled(context.Context, slog.Level) bool { return true }

func (t *LogTail) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Time.Format("15:04:05"))
	b.WriteByte(' ')
	b.WriteString(r.Level.String())
	b.WriteByte(' ')
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value.Any())
		return true
	})

	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, b.String())
	if len(t.lines) > t.max {
		t.lines = t.lines[len(t.lines)-t.max:]
	}
	return nil
}

// WithAttrs/WithGroup return the handler unchanged: nothing in this server
// calls slog.Logger.With/WithGroup today, it's always the flat
// log.Info("msg", "k", v) shape Handle already folds into one line.
//
// ponytail: if a future caller does start chaining .With(), those attrs
// would silently not appear in the tail — add real attr/group threading
// (embed a slog.HandlerOptions-style prefix) if that happens.
func (t *LogTail) WithAttrs(attrs []slog.Attr) slog.Handler { return t }
func (t *LogTail) WithGroup(name string) slog.Handler       { return t }

// Lines returns a snapshot of the current tail, oldest first.
func (t *LogTail) Lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.lines))
	copy(out, t.lines)
	return out
}
