package telemetry

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"mhotas/pkg/protocol"
)

type nopSink struct{ n int }

func (s *nopSink) SendTelemetry(protocol.Telemetry) error { s.n++; return nil }

func TestLogChangesSkipsUnchangedAndGroundSpeed(t *testing.T) {
	var buf bytes.Buffer
	sink := &nopSink{}
	l := LogChanges(sink, slog.New(slog.NewTextHandler(&buf, nil)))

	l.SendTelemetry(protocol.Telemetry{SpoilersHandle: 0.5})
	l.SendTelemetry(protocol.Telemetry{SpoilersHandle: 0.501, GroundSpeedKnots: 40})
	l.SendTelemetry(protocol.Telemetry{SpoilersHandle: 1})

	if sink.n != 3 {
		t.Fatalf("forwarded %d packets, want 3", sink.n)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], "spoilers=1 ") {
		t.Fatalf("log = %q, want 2 lines, the second with spoilers=1", lines)
	}
}
