package transport

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"mhotas/internal/config"
	"mhotas/internal/control"
	"mhotas/pkg/protocol"
)

type axisCall struct {
	axis  control.AxisID
	value float64
}

type buttonCall struct {
	id      control.ButtonID
	pressed bool
}

// fakeBackend is an in-memory control.Output for testing the transport
// layer without needing SimConnect or MSFS.
type fakeBackend struct {
	mu      sync.Mutex
	axes    []axisCall
	buttons []buttonCall
}

func (b *fakeBackend) Name() string { return "fake" }

func (b *fakeBackend) SetAxis(axis control.AxisID, value float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.axes = append(b.axes, axisCall{axis, value})
	return nil
}

func (b *fakeBackend) SetButton(id control.ButtonID, pressed bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buttons = append(b.buttons, buttonCall{id, pressed})
	return nil
}

func (b *fakeBackend) Close() error { return nil }

func (b *fakeBackend) lastAxis(axis control.AxisID) (float64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.axes) - 1; i >= 0; i-- {
		if b.axes[i].axis == axis {
			return b.axes[i].value, true
		}
	}
	return 0, false
}

// axisHistory returns every value received for axis, in call order.
func (b *fakeBackend) axisHistory(axis control.AxisID) []float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []float64
	for _, c := range b.axes {
		if c.axis == axis {
			out = append(out, c.value)
		}
	}
	return out
}

func (b *fakeBackend) buttonCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buttons)
}

func newTestServer(t *testing.T, cfg config.Config) (*Server, *fakeBackend, *net.UDPConn) {
	t.Helper()
	if cfg.ListenPort == 0 {
		// let the OS pick a free port
	}
	backend := &fakeBackend{}
	log := slog.New(slog.DiscardHandler)
	srv, err := New(cfg, backend, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		if err := srv.Run(ctx); err != nil {
			t.Logf("Run exited: %v", err)
		}
	}()

	client, err := net.DialUDP("udp", nil, srv.Addr())
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	return srv, backend, client
}

func sendHello(t *testing.T, client *net.UDPConn) {
	t.Helper()
	pkt := protocol.EncodeHello(1, protocol.Hello{DeviceName: "test-phone"})
	if _, err := client.Write(pkt); err != nil {
		t.Fatalf("write HELLO: %v", err)
	}

	buf := make([]byte, 1500)
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read HELLO_ACK: %v", err)
	}
	ack, err := protocol.DecodeHelloAck(buf[:n])
	if err != nil {
		t.Fatalf("DecodeHelloAck: %v", err)
	}
	if ack.Status != protocol.HelloOK {
		t.Fatalf("HELLO_ACK status = %v, want HelloOK", ack.Status)
	}
}

func TestHelloEstablishesConnection(t *testing.T) {
	cfg := config.Default()
	cfg.ListenPort = 0
	_, _, client := newTestServer(t, cfg)
	sendHello(t, client)
}

func TestAxisAppliesCalibrationAndReachesBackend(t *testing.T) {
	cfg := config.Default()
	cfg.ListenPort = 0
	cfg.Pitch.Curve.Invert = true
	_, backend, client := newTestServer(t, cfg)
	sendHello(t, client)

	pkt := protocol.EncodeAxis(2, protocol.Axis{Pitch: 0.5, Roll: 0.25, Throttle: 0.8, Rudder: -0.3})
	if _, err := client.Write(pkt); err != nil {
		t.Fatalf("write AXIS: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := backend.lastAxis(control.AxisPitch); ok {
			if v != -0.5 {
				t.Fatalf("pitch = %v, want -0.5 (inverted)", v)
			}
			roll, _ := backend.lastAxis(control.AxisRoll)
			if roll != 0.25 {
				t.Fatalf("roll = %v, want 0.25", roll)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for backend to receive AXIS values")
}

func TestButtonDeduplicatesByEventID(t *testing.T) {
	cfg := config.Default()
	cfg.ListenPort = 0
	_, backend, client := newTestServer(t, cfg)
	sendHello(t, client)

	// Same event sent 3x, as the real app does for redundancy.
	for i := range 3 {
		pkt := protocol.EncodeButton(uint16(i+2), protocol.Button{ButtonID: 1, Pressed: true, EventID: 100})
		if _, err := client.Write(pkt); err != nil {
			t.Fatalf("write BUTTON: %v", err)
		}
	}
	// A stale/older event ID should also be dropped.
	pkt := protocol.EncodeButton(5, protocol.Button{ButtonID: 1, Pressed: false, EventID: 99})
	if _, err := client.Write(pkt); err != nil {
		t.Fatalf("write BUTTON: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if got := backend.buttonCount(); got != 1 {
		t.Fatalf("backend received %d button calls, want 1 (deduped)", got)
	}
}

func TestAxisSmoothingDampensJump(t *testing.T) {
	cfg := config.Default()
	cfg.ListenPort = 0
	cfg.Pitch.Smoothing = 0.5
	_, backend, client := newTestServer(t, cfg)
	sendHello(t, client)

	waitForCount := func(n int) []float64 {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if hist := backend.axisHistory(control.AxisPitch); len(hist) >= n {
				return hist
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %d pitch samples", n)
		return nil
	}

	pkt := protocol.EncodeAxis(2, protocol.Axis{Pitch: 1})
	if _, err := client.Write(pkt); err != nil {
		t.Fatalf("write AXIS: %v", err)
	}
	hist := waitForCount(1)
	if hist[0] != 0.5 {
		t.Fatalf("first smoothed pitch = %v, want 0.5 (halfway from 0 to a raw jump to 1)", hist[0])
	}

	pkt = protocol.EncodeAxis(3, protocol.Axis{Pitch: 1})
	if _, err := client.Write(pkt); err != nil {
		t.Fatalf("write AXIS: %v", err)
	}
	hist = waitForCount(2)
	if hist[1] != 0.75 {
		t.Fatalf("second smoothed pitch = %v, want 0.75 (still converging toward 1)", hist[1])
	}
}

func TestFailsafeNeutralizesAxisAfterTimeout(t *testing.T) {
	cfg := config.Default()
	cfg.ListenPort = 0
	cfg.FailsafeTimeoutMS = 100
	_, backend, client := newTestServer(t, cfg)
	sendHello(t, client)

	pkt := protocol.EncodeAxis(2, protocol.Axis{Pitch: 1, Roll: 1, Throttle: 1, Rudder: 1})
	if _, err := client.Write(pkt); err != nil {
		t.Fatalf("write AXIS: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pitch, _ := backend.lastAxis(control.AxisPitch)
		throttle, _ := backend.lastAxis(control.AxisThrottle)
		if pitch == 0 && throttle == 1 {
			// pitch neutralized, throttle held at its last value — matches
			// the default failsafe policy.
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for failsafe to neutralize pitch while holding throttle")
}
