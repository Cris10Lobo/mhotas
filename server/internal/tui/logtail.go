// Package tui is the server's read-only terminal dashboard.
package tui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// LogTail is an slog.Handler that keeps the last max formatted lines in memory.
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

// ponytail: WithAttrs/WithGroup are no-ops; attrs from Logger.With would be dropped. Nothing uses With today.
func (t *LogTail) WithAttrs([]slog.Attr) slog.Handler { return t }
func (t *LogTail) WithGroup(string) slog.Handler      { return t }

// Lines returns the tail, oldest first.
func (t *LogTail) Lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.lines...)
}
