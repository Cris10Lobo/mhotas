package transport

import (
	"context"
	"log/slog"
	"math"
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

func (b *fakeBackend) Ready() bool { return true }

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
	return newTestServerWithConfigPath(t, cfg, "")
}

func newTestServerWithConfigPath(t *testing.T, cfg config.Config, configPath string) (*Server, *fakeBackend, *net.UDPConn) {
	t.Helper()
	if cfg.ListenPort == 0 {
	}
	backend := &fakeBackend{}
	log := slog.New(slog.DiscardHandler)
	srv, err := New(cfg, backend, log, configPath)
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

	pkt := protocol.EncodeAxis(2, protocol.Axis{Pitch: 0.5, Roll: 0.25, Throttle: 0.8, Rudder: -0.3, Speedbrake: 1.5})
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
			if sb, _ := backend.lastAxis(control.AxisSpeedbrake); sb != 1 {
				t.Fatalf("speedbrake = %v, want 1 (clamped)", sb)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for backend to receive AXIS values")
}

func TestUntouchedThrottleAndSpeedbrakeAreNotSent(t *testing.T) {
	cfg := config.Default()
	cfg.ListenPort = 0
	cfg.Speedbrake.Curve.Invert = true
	_, backend, client := newTestServer(t, cfg)
	sendHello(t, client)

	nan := float32(math.NaN())
	pkt := protocol.EncodeAxis(2, protocol.Axis{Pitch: 0.5, Throttle: nan, Speedbrake: nan})
	if _, err := client.Write(pkt); err != nil {
		t.Fatalf("write AXIS: %v", err)
	}
	waitAxis(t, backend, control.AxisPitch)
	if _, ok := backend.lastAxis(control.AxisThrottle); ok {
		t.Fatal("NaN throttle reached the backend")
	}
	if _, ok := backend.lastAxis(control.AxisSpeedbrake); ok {
		t.Fatal("NaN speedbrake reached the backend")
	}

	// First real value is sent, with the speedbrake inverted.
	pkt = protocol.EncodeAxis(3, protocol.Axis{Throttle: nan, Speedbrake: 0.25})
	if _, err := client.Write(pkt); err != nil {
		t.Fatalf("write AXIS: %v", err)
	}
	if got := waitAxis(t, backend, control.AxisSpeedbrake); got != 0.75 {
		t.Fatalf("speedbrake = %v, want 0.75 (inverted)", got)
	}
}

func waitAxis(t *testing.T, backend *fakeBackend, id control.AxisID) float64 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := backend.lastAxis(id); ok {
			return v
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %v", id)
	return 0
}

func TestButtonDeduplicatesByEventID(t *testing.T) {
	cfg := config.Default()
	cfg.ListenPort = 0
	_, backend, client := newTestServer(t, cfg)
	sendHello(t, client)

	for i := range 3 {
		pkt := protocol.EncodeButton(uint16(i+2), protocol.Button{ButtonID: 1, Pressed: true, EventID: 100})
		if _, err := client.Write(pkt); err != nil {
			t.Fatalf("write BUTTON: %v", err)
		}
	}
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

func TestConfigSetUpdatesCalibrationPersistsAndEchoes(t *testing.T) {
	cfg := config.Default()
	cfg.ListenPort = 0
	configPath := t.TempDir() + "/config.json"
	_, backend, client := newTestServerWithConfigPath(t, cfg, configPath)
	sendHello(t, client)

	readConfig := func() protocol.Config {
		t.Helper()
		buf := make([]byte, 1500)
		if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("SetReadDeadline: %v", err)
		}
		n, err := client.Read(buf)
		if err != nil {
			t.Fatalf("read CONFIG: %v", err)
		}
		c, err := protocol.DecodeConfig(buf[:n])
		if err != nil {
			t.Fatalf("DecodeConfig: %v", err)
		}
		return c
	}
	readConfig()

	pkt := protocol.EncodeConfigSet(2, protocol.ConfigSet{
		Axis: protocol.ConfigAxisThrottle,
		Calibration: protocol.AxisCalibration{
			DeadZone: 0.2, Expo: 0.5, Invert: true, Smoothing: 0.1,
		},
	})
	if _, err := client.Write(pkt); err != nil {
		t.Fatalf("write CONFIG_SET: %v", err)
	}

	got := readConfig()
	want := protocol.AxisCalibration{DeadZone: 0.2, Expo: 0.5, Invert: true, Smoothing: 0.1}
	if got.Throttle != want {
		t.Fatalf("echoed throttle calibration = %+v, want %+v", got.Throttle, want)
	}

	// A subsequent AXIS packet should be shaped by the new calibration
	// (inverted, so a positive raw value comes out negative).
	pkt = protocol.EncodeAxis(3, protocol.Axis{Throttle: 0.5})
	if _, err := client.Write(pkt); err != nil {
		t.Fatalf("write AXIS: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := backend.lastAxis(control.AxisThrottle); ok {
			if v >= 0 {
				t.Fatalf("throttle = %v, want negative (new invert applied)", v)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load after CONFIG_SET: %v", err)
	}
	if loaded.Throttle.Curve.DeadZone != 0.2 ||
		loaded.Throttle.Curve.Expo != 0.5 ||
		!loaded.Throttle.Curve.Invert ||
		loaded.Throttle.Smoothing != 0.1 {
		t.Fatalf("persisted throttle config = %+v, want dead_zone 0.2, expo 0.5, invert true, smoothing 0.1", loaded.Throttle)
	}
	// Failsafe policy, which CONFIG_SET never touches, must survive
	// unchanged.
	if loaded.Throttle.Failsafe != config.FailsafeHold {
		t.Fatalf("persisted throttle failsafe = %v, want unchanged FailsafeHold", loaded.Throttle.Failsafe)
	}
}

func TestConfigGetRepliesWithCurrentConfig(t *testing.T) {
	cfg := config.Default()
	cfg.ListenPort = 0
	cfg.Rudder.Curve.DeadZone = 0.05
	_, _, client := newTestServer(t, cfg)
	sendHello(t, client)

	buf := make([]byte, 1500)
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := client.Read(buf); err != nil {
		t.Fatalf("read initial CONFIG: %v", err)
	}

	pkt := protocol.EncodeConfigGet(2)
	if _, err := client.Write(pkt); err != nil {
		t.Fatalf("write CONFIG_GET: %v", err)
	}
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read CONFIG reply: %v", err)
	}
	got, err := protocol.DecodeConfig(buf[:n])
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if got.Rudder.DeadZone != float32(0.05) {
		t.Fatalf("rudder deadZone = %v, want 0.05", got.Rudder.DeadZone)
	}
}

func TestFailsafeNeutralizesAxisAfterTimeout(t *testing.T) {
	cfg := config.Default()
	cfg.ListenPort = 0
	cfg.FailsafeTimeoutMS = 100
	_, backend, client := newTestServer(t, cfg)
	sendHello(t, client)

	pkt := protocol.EncodeAxis(2, protocol.Axis{Pitch: 1, Roll: 1, Throttle: 1, Rudder: 1, Speedbrake: 0.5})
	if _, err := client.Write(pkt); err != nil {
		t.Fatalf("write AXIS: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pitch, _ := backend.lastAxis(control.AxisPitch)
		throttle, _ := backend.lastAxis(control.AxisThrottle)
		speedbrake, _ := backend.lastAxis(control.AxisSpeedbrake)
		if pitch == 0 && throttle == 1 && speedbrake == 0.5 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for failsafe to neutralize pitch while holding throttle")
}

type signedRecorder struct {
	fakeBackend
	signed bool
	idle   float64
	invert bool
}

func (b *signedRecorder) SetThrottleInvert(invert bool) {
	b.mu.Lock()
	b.invert = invert
	b.mu.Unlock()
}

func (b *signedRecorder) SetSignedThrottle(signed bool) {
	b.mu.Lock()
	b.signed = signed
	b.mu.Unlock()
}

func (b *signedRecorder) SetSignedThrottleIdle(idle float64) {
	b.mu.Lock()
	b.idle = idle
	b.mu.Unlock()
}

func (b *signedRecorder) settings() (bool, float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.signed, b.idle
}

func (b *signedRecorder) inverted() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.invert
}

func TestSignedThrottleSettingsReachBackend(t *testing.T) {
	cfg := config.Default()
	cfg.ListenPort = 0
	cfg.Throttle.SignedThrottle = true
	cfg.Throttle.SignedThrottleIdle = -0.25
	vj := &signedRecorder{}
	srv, err := New(cfg, vj, slog.New(slog.DiscardHandler), "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if signed, idle := vj.settings(); !signed || idle != -0.25 {
		t.Fatalf("startup: got signed=%v idle=%v, want true -0.25 from config", signed, idle)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Run(ctx)
	client, err := net.DialUDP("udp", nil, srv.Addr())
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	sendHello(t, client)

	pkt := protocol.EncodeConfigSet(2, protocol.ConfigSet{
		Axis:        protocol.ConfigAxisThrottle,
		Calibration: protocol.AxisCalibration{SignedThrottle: true, SignedThrottleIdle: -0.45, Invert: true},
	})
	if _, err := client.Write(pkt); err != nil {
		t.Fatalf("write CONFIG_SET: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if signed, idle := vj.settings(); signed && math.Abs(idle+0.45) < 1e-6 && vj.inverted() {
			// The backend reverses the axis itself, so transport must not
			// also invert the value it sends (that swapped forward/reverse
			// zones around idle).
			pkt = protocol.EncodeAxis(3, protocol.Axis{Throttle: 0.5})
			if _, err := client.Write(pkt); err != nil {
				t.Fatalf("write AXIS: %v", err)
			}
			for time.Now().Before(deadline) {
				if v, ok := vj.lastAxis(control.AxisThrottle); ok {
					if v != 0.5 {
						t.Fatalf("throttle reached backend as %v, want 0.5 (uninverted)", v)
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("timed out waiting for throttle AXIS")
		}
		time.Sleep(10 * time.Millisecond)
	}
	signed, idle := vj.settings()
	t.Fatalf("after CONFIG_SET: got signed=%v idle=%v, want true -0.45", signed, idle)
}

// The phone lever must show the input that produces the sim's lever
// position: expo 0.5 turns a 0.5 input into 0.3125, so a sim lever at
// 0.3125 must reach the phone as 0.5.
func TestTelemetryThrottleUndoesCurve(t *testing.T) {
	cfg := config.Default()
	cfg.ListenPort = 0
	cfg.Throttle.Curve.Expo = 0.5
	srv, _, client := newTestServer(t, cfg)
	sendHello(t, client)

	if err := srv.SendTelemetry(protocol.Telemetry{ThrottlePercent: 0.3125}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1500)
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		n, err := client.Read(buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if h, err := protocol.DecodeHeader(buf[:n]); err != nil || h.Type != protocol.TypeTelemetry {
			continue // CONFIG etc.
		}
		got, err := protocol.DecodeTelemetry(buf[:n])
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(float64(got.ThrottlePercent)-0.5) > 1e-4 {
			t.Fatalf("phone got throttle %v, want 0.5", got.ThrottlePercent)
		}
		return
	}
}

func TestWidenKeepsShortestDecimal(t *testing.T) {
	if got := widen(float32(0.05)); got != 0.05 {
		t.Fatalf("widen(float32(0.05)) = %v, want 0.05", got)
	}
}
