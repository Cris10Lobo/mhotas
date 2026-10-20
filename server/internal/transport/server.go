// Package transport owns the UDP socket: it turns incoming phone packets
// into calibrated control.Output calls, tracks the connected phone's
// address for pushing TELEMETRY/HEARTBEAT back, and runs the failsafe
// timer.
//
// MVP supports a single active phone connection at a time, matching how
// the closest prior art (Fly-By-Wireless) operates.
package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"sync"
	"time"

	"mhotas/internal/calibration"
	"mhotas/internal/config"
	"mhotas/internal/control"
	"mhotas/pkg/protocol"
)

// Server reads phone packets, drives a control.Output backend, and pushes
// telemetry/heartbeats back to whichever phone last sent a valid HELLO.
type Server struct {
	conn       *net.UDPConn
	backend    control.Output
	log        *slog.Logger
	configPath string // for config.Save on a live CONFIG_SET; "" = don't persist

	// cfg is mutated at runtime by CONFIG_SET (from the single-goroutine
	// receive loop) and read concurrently by failsafeLoop (its own
	// goroutine) — every access goes through axisConfig/currentConfig/
	// setAxisConfig below, all mu-guarded.
	cfg config.Config

	mu                sync.Mutex
	clientAddr        *net.UDPAddr
	deviceName        string
	lastRxAt          time.Time
	lastAxisAt        time.Time
	failsafeApplied   bool
	lastButtonEvent   map[uint8]uint16
	lastShaped        map[control.AxisID]float64
	outSeq            uint16
	lastBackendWarnAt time.Time
}

// deviceLivenessTimeout mirrors the app's own UdpClient liveness check
// (app/lib/network/udp_client.dart) — past this long since any valid packet
// from the phone, Status() reports it disconnected rather than showing a
// stale "still connected" from before a WiFi drop or the app closing.
const deviceLivenessTimeout = 3 * time.Second

// Status is a point-in-time snapshot of what the server is doing right now
// — for display (the TUI), not for control-flow decisions, which all live
// on the fields above directly.
type Status struct {
	ListenAddr      string
	DeviceName      string
	DeviceConnected bool
	LastPhoneSeen   time.Time
}

func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{
		ListenAddr:      s.conn.LocalAddr().String(),
		DeviceName:      s.deviceName,
		DeviceConnected: s.clientAddr != nil && time.Since(s.lastRxAt) < deviceLivenessTimeout,
		LastPhoneSeen:   s.lastRxAt,
	}
}

// New starts listening on cfg.ListenPort. configPath is the file cfg was
// loaded from (empty if none) — remembered so a live CONFIG_SET can persist
// back to it; New itself never reads or writes the file again.
func New(cfg config.Config, backend control.Output, log *slog.Logger, configPath string) (*Server, error) {
	// "udp4", not "udp": phones connect via manual IPv4 entry, and an
	// unspecified bind on Windows can land on the IPv6 wildcard, which
	// WSL's IPv4 loopback forwarding can't reach ("connection refused").
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: cfg.ListenPort})
	if err != nil {
		return nil, fmt.Errorf("transport: listen on port %d: %w", cfg.ListenPort, err)
	}
	if setter, ok := backend.(control.SignedThrottleSetter); ok {
		setter.SetSignedThrottle(cfg.Throttle.SignedThrottle)
		setter.SetSignedThrottleIdle(cfg.Throttle.SignedThrottleIdle)
		setter.SetThrottleInvert(cfg.Throttle.Curve.Invert)
	}
	return &Server{
		conn:            conn,
		backend:         backend,
		cfg:             cfg,
		log:             log,
		configPath:      configPath,
		lastButtonEvent: make(map[uint8]uint16),
		lastShaped:      make(map[control.AxisID]float64),
	}, nil
}

// SyncThrottle implements control.ThrottleSyncer for telemetry: the sim's
// lever position goes through the throttle's calibration, exactly like a
// phone value at that position would, before reaching the backend. The
// phone lever shows the sim's position and sends from it, so this is what
// the backend would hold had the phone put it there. Skipping the curve
// writes the wrong value — with invert on, the mirror image, which the sim
// then jumps to.
func (s *Server) SyncThrottle(sim float64, autothrottle bool) {
	if ts, ok := s.backend.(control.ThrottleSyncer); ok {
		ts.SyncThrottle(s.curve(control.AxisThrottle, s.axisConfig(control.AxisThrottle)).Apply(sim), autothrottle)
	}
}

// curve is the calibration curve transport itself applies. Throttle invert
// is left out when the backend takes it over (control.SignedThrottleSetter):
// the backend reverses the final output after its idle remap, which is
// what invert has to mean when idle isn't at the axis center.
func (s *Server) curve(id control.AxisID, ac config.AxisConfig) calibration.Curve {
	c := ac.Curve
	if _, ok := s.backend.(control.SignedThrottleSetter); ok && id == control.AxisThrottle {
		c.Invert = false
	}
	return c
}

// axisConfig returns the current calibration for one axis. Mu-guarded since
// CONFIG_SET can mutate cfg concurrently with failsafeLoop reading it.
func (s *Server) axisConfig(id control.AxisID) config.AxisConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch id {
	case control.AxisPitch:
		return s.cfg.Pitch
	case control.AxisRoll:
		return s.cfg.Roll
	case control.AxisThrottle:
		return s.cfg.Throttle
	case control.AxisRudder:
		return s.cfg.Rudder
	default:
		return config.AxisConfig{}
	}
}

// currentConfig returns a snapshot of the full config, e.g. to persist or
// send back to the phone.
func (s *Server) currentConfig() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// setAxisConfig applies new dead-zone/expo/invert/smoothing to one axis,
// preserving that axis's existing Failsafe policy — CONFIG_SET never
// touches Failsafe, which stays a config-file-only, operator judgment call.
// Reports the axis's control.AxisID and whether the selector was valid.
func (s *Server) setAxisConfig(axis protocol.ConfigAxis, ac config.AxisConfig) (control.AxisID, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch axis {
	case protocol.ConfigAxisPitch:
		ac.Failsafe = s.cfg.Pitch.Failsafe
		s.cfg.Pitch = ac
		return control.AxisPitch, true
	case protocol.ConfigAxisRoll:
		ac.Failsafe = s.cfg.Roll.Failsafe
		s.cfg.Roll = ac
		return control.AxisRoll, true
	case protocol.ConfigAxisThrottle:
		ac.Failsafe = s.cfg.Throttle.Failsafe
		s.cfg.Throttle = ac
		return control.AxisThrottle, true
	case protocol.ConfigAxisRudder:
		ac.Failsafe = s.cfg.Rudder.Failsafe
		s.cfg.Rudder = ac
		return control.AxisRudder, true
	default:
		return 0, false
	}
}

func (s *Server) Addr() *net.UDPAddr {
	return s.conn.LocalAddr().(*net.UDPAddr)
}

func (s *Server) Run(ctx context.Context) error {
	go s.failsafeLoop(ctx)
	go s.heartbeatLoop(ctx)

	buf := make([]byte, 1500)
	for {
		if ctx.Err() != nil {
			return s.conn.Close()
		}
		if err := s.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			return fmt.Errorf("transport: set read deadline: %w", err)
		}
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			s.log.Warn("read error", "err", err)
			continue
		}
		s.safeHandlePacket(addr, buf[:n])
	}
}

// safeHandlePacket recovers from a panic in handlePacket (a malformed
// packet hitting an edge case in decode/calibration/backend code, say) so
// one bad packet logs and gets dropped instead of taking the whole server
// — and every pilot's control input — down mid-flight.
func (s *Server) safeHandlePacket(addr *net.UDPAddr, data []byte) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("panic handling packet, dropped", "from", addr, "recovered", r)
		}
	}()
	s.handlePacket(addr, data)
}

func (s *Server) handlePacket(addr *net.UDPAddr, data []byte) {
	hdr, err := protocol.DecodeHeader(data)
	if err != nil {
		s.log.Warn("bad header", "err", err, "from", addr)
		return
	}
	s.mu.Lock()
	s.lastRxAt = time.Now()
	s.mu.Unlock()
	// Only HELLO gets a version-mismatch reply; other stray packets from an
	// incompatible client are just dropped rather than acted on.
	if hdr.Version != protocol.Version && hdr.Type != protocol.TypeHello {
		s.log.Warn("dropping non-HELLO packet with version mismatch", "type", hdr.Type, "from", addr)
		return
	}

	switch hdr.Type {
	case protocol.TypeHello:
		s.handleHello(addr, hdr, data)
	case protocol.TypeAxis:
		s.handleAxis(addr, data)
	case protocol.TypeButton:
		s.handleButton(addr, data)
	case protocol.TypeConfigSet:
		s.handleConfigSet(addr, data)
	case protocol.TypeConfigGet:
		s.handleConfigGet(addr)
	case protocol.TypeHeartbeat:
		s.log.Debug("heartbeat", "from", addr)
	default:
		s.log.Warn("unexpected packet type from phone", "type", hdr.Type, "from", addr)
	}
}

func (s *Server) handleHello(addr *net.UDPAddr, hdr protocol.Header, data []byte) {
	hello, err := protocol.DecodeHello(data)
	if err != nil {
		s.log.Warn("bad HELLO", "err", err, "from", addr)
		return
	}

	status := protocol.HelloOK
	if hdr.Version != protocol.Version {
		status = protocol.HelloVersionMismatch
		s.log.Warn("HELLO version mismatch", "device", hello.DeviceName,
			"gotVersion", hdr.Version, "wantVersion", protocol.Version)
	} else {
		s.log.Info("phone connected", "device", hello.DeviceName, "from", addr)
	}

	if status == protocol.HelloOK {
		s.mu.Lock()
		s.clientAddr = addr
		s.deviceName = hello.DeviceName
		s.lastAxisAt = time.Now()
		s.failsafeApplied = false
		s.lastButtonEvent = make(map[uint8]uint16)
		s.lastShaped = make(map[control.AxisID]float64)
		s.mu.Unlock()
	}

	ack := protocol.EncodeHelloAck(s.nextSeq(), protocol.HelloAck{Status: status, ServerName: "mhotas-server"})
	if _, err := s.conn.WriteToUDP(ack, addr); err != nil {
		s.log.Warn("failed to send HELLO_ACK", "err", err)
	}

	// Send current calibration right away so a freshly-opened settings
	// screen shows real values instead of defaults — no separate
	// CONFIG_GET request type needed for that.
	if status == protocol.HelloOK {
		if err := s.SendConfig(); err != nil {
			s.log.Warn("failed to send CONFIG", "err", err)
		}
	}
}

func (s *Server) handleAxis(addr *net.UDPAddr, data []byte) {
	if !s.isCurrentClient(addr) {
		s.log.Warn("dropping AXIS from non-established address", "from", addr)
		return
	}
	axis, err := protocol.DecodeAxis(data)
	if err != nil {
		s.log.Warn("bad AXIS", "err", err, "from", addr)
		return
	}

	s.mu.Lock()
	s.lastAxisAt = time.Now()
	s.failsafeApplied = false
	s.mu.Unlock()

	s.applyAxis(control.AxisPitch, float64(axis.Pitch), s.axisConfig(control.AxisPitch))
	s.applyAxis(control.AxisRoll, float64(axis.Roll), s.axisConfig(control.AxisRoll))
	s.applyAxis(control.AxisThrottle, float64(axis.Throttle), s.axisConfig(control.AxisThrottle))
	s.applyAxis(control.AxisRudder, float64(axis.Rudder), s.axisConfig(control.AxisRudder))
}

func (s *Server) applyAxis(id control.AxisID, raw float64, ac config.AxisConfig) {
	shaped, changed := s.smooth(id, s.curve(id, ac).Apply(raw), ac.Smoothing)
	if !changed {
		// Value hasn't moved since the last packet — don't re-assert it.
		// Without this, mhotas would resend the same throttle/rudder value
		// to the backend 30-50x/sec regardless of what the sim itself is
		// now showing, fighting any adjustment made directly in the sim
		// (dragging the throttle with a mouse, autothrottle, etc.) within
		// one packet interval. Pitch/roll are unaffected in practice —
		// sensor noise means they essentially never repeat bit-for-bit.
		return
	}
	if err := s.backend.SetAxis(id, shaped); err != nil {
		s.warnBackendFailure("backend SetAxis failed", "axis", id, "err", err)
	}
}

// backendWarnInterval throttles repeated backend-write-failure logging
// (e.g. MSFS2024 not running yet) to once per interval instead of once per
// call — AXIS packets arrive 30-50x/sec across 4 axes, which floods the log
// within a second of the backend becoming unreachable.
const backendWarnInterval = 5 * time.Second

func (s *Server) warnBackendFailure(msg string, args ...any) {
	s.mu.Lock()
	throttled := time.Since(s.lastBackendWarnAt) < backendWarnInterval
	if !throttled {
		s.lastBackendWarnAt = time.Now()
	}
	s.mu.Unlock()
	if !throttled {
		s.log.Warn(msg, args...)
	}
}

// changeEpsilon is the tolerance smooth uses to decide an axis has
// "settled". An EMA only asymptotically approaches its target — for any
// smoothing > 0, exact float equality to the previous output is reached
// only after enough iterations for the residual to underflow, so a steady
// raw input would otherwise keep reporting changed=true (and re-fighting
// any manual in-sim adjustment) for longer than intended after each move.
const changeEpsilon = 1e-6

// smooth blends shaped with the previous call's result for this axis:
// smoothing=0 returns shaped unchanged (default, no behavior change),
// closer to 1 leans more on the previous value, damping a fast/jerky
// swing into a gradual move instead of a raw spike. changed reports
// whether the result differs meaningfully from the last value smooth
// returned for this axis, so callers can skip re-sending an unchanged
// value — see changeEpsilon for why this isn't exact equality.
func (s *Server) smooth(id control.AxisID, shaped, smoothing float64) (out float64, changed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, seen := s.lastShaped[id]
	out = shaped*(1-smoothing) + prev*smoothing
	s.lastShaped[id] = out
	return out, !seen || math.Abs(out-prev) > changeEpsilon
}

func (s *Server) handleButton(addr *net.UDPAddr, data []byte) {
	if !s.isCurrentClient(addr) {
		s.log.Warn("dropping BUTTON from non-established address", "from", addr)
		return
	}
	btn, err := protocol.DecodeButton(data)
	if err != nil {
		s.log.Warn("bad BUTTON", "err", err, "from", addr)
		return
	}

	s.mu.Lock()
	last, seen := s.lastButtonEvent[btn.ButtonID]
	// int16 subtraction handles the uint16 wraparound: if btn.EventID is
	// "ahead of" last in circular sequence terms, the difference is positive.
	isNewer := !seen || int16(btn.EventID-last) > 0
	if isNewer {
		s.lastButtonEvent[btn.ButtonID] = btn.EventID
	}
	s.mu.Unlock()

	if !isNewer {
		return
	}
	if err := s.backend.SetButton(control.ButtonID(btn.ButtonID), btn.Pressed); err != nil {
		s.warnBackendFailure("backend SetButton failed", "button", btn.ButtonID, "err", err)
		return
	}
	// Buttons are rare, discrete events (unlike the 30Hz AXIS stream), so
	// logging every one isn't spam — it's the only way to see a press
	// actually reached and succeeded, since backends stay silent on success.
	s.log.Info("button", "id", btn.ButtonID, "pressed", btn.Pressed)
}

// handleConfigSet applies a phone-edited dead-zone/expo/invert/smoothing to
// one axis, persists it (if a config file path is known), and echoes back
// the full CONFIG snapshot so the phone's settings screen shows exactly
// what's now in effect.
func (s *Server) handleConfigSet(addr *net.UDPAddr, data []byte) {
	if !s.isCurrentClient(addr) {
		s.log.Warn("dropping CONFIG_SET from non-established address", "from", addr)
		return
	}
	cs, err := protocol.DecodeConfigSet(data)
	if err != nil {
		s.log.Warn("bad CONFIG_SET", "err", err, "from", addr)
		return
	}
	// DeadZone/Expo/Smoothing are only ever meaningful in [0,1] — Curve.Apply
	// only guards DeadZone>=1, so an out-of-range value from the wire (a
	// stale app build, a malformed packet) needs clamping here, the actual
	// trust boundary, rather than trusted to already be sane.
	ac := config.AxisConfig{
		Curve: calibration.Curve{
			DeadZone: clamp01(float64(cs.Calibration.DeadZone)),
			Expo:     clamp01(float64(cs.Calibration.Expo)),
			Invert:   cs.Calibration.Invert,
		},
		Smoothing:          clamp01(float64(cs.Calibration.Smoothing)),
		SignedThrottle:     cs.Calibration.SignedThrottle,
		SignedThrottleIdle: clampSigned(float64(cs.Calibration.SignedThrottleIdle)),
	}
	id, ok := s.setAxisConfig(cs.Axis, ac)
	if !ok {
		s.log.Warn("bad CONFIG_SET axis", "axis", cs.Axis, "from", addr)
		return
	}
	// Only throttle's driver has anything to switch (see
	// control.SignedThrottleSetter) — a no-op type-assertion failure for
	// every other axis and every backend without a throttle driver that
	// cares (logbackend, vjoy).
	if id == control.AxisThrottle {
		if setter, ok := s.backend.(control.SignedThrottleSetter); ok {
			setter.SetSignedThrottle(ac.SignedThrottle)
			setter.SetSignedThrottleIdle(ac.SignedThrottleIdle)
			setter.SetThrottleInvert(ac.Curve.Invert)
		}
	}
	if err := config.Save(s.configPath, s.currentConfig()); err != nil {
		s.log.Warn("failed to persist config", "err", err)
	}
	s.log.Info("config updated", "axis", id, "deadZone", ac.Curve.DeadZone,
		"expo", ac.Curve.Expo, "invert", ac.Curve.Invert, "smoothing", ac.Smoothing,
		"signedThrottle", ac.SignedThrottle, "signedThrottleIdle", ac.SignedThrottleIdle)
	if err := s.SendConfig(); err != nil {
		s.log.Warn("failed to send CONFIG", "err", err)
	}
}

// handleConfigGet replies with the current CONFIG snapshot — a settings
// screen that mounts after the one unprompted post-HELLO_ACK CONFIG already
// fired (and was missed, since nothing has a "last value" buffer for it the
// way TELEMETRY's recurring stream implicitly does) sends this to catch up.
func (s *Server) handleConfigGet(addr *net.UDPAddr) {
	if !s.isCurrentClient(addr) {
		s.log.Warn("dropping CONFIG_GET from non-established address", "from", addr)
		return
	}
	if err := s.SendConfig(); err != nil {
		s.log.Warn("failed to send CONFIG", "err", err)
	}
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// clampSigned is clamp01's -1..1 counterpart, for SignedThrottleIdle — a
// wire value, same trust-boundary reasoning as clamp01.
func clampSigned(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

// SendTelemetry pushes a TELEMETRY packet to the currently connected phone,
// if any. Safe to call with no phone connected (no-op).
func (s *Server) SendTelemetry(t protocol.Telemetry) error {
	addr := s.currentClient()
	if addr == nil {
		return nil
	}
	pkt := protocol.EncodeTelemetry(s.nextSeq(), t)
	_, err := s.conn.WriteToUDP(pkt, addr)
	return err
}

// SendConfig pushes the current calibration to the connected phone, if any
// — sent right after HELLO_ACK and again after every applied CONFIG_SET.
func (s *Server) SendConfig() error {
	addr := s.currentClient()
	if addr == nil {
		return nil
	}
	toWire := func(ac config.AxisConfig) protocol.AxisCalibration {
		return protocol.AxisCalibration{
			DeadZone:           float32(ac.Curve.DeadZone),
			Expo:               float32(ac.Curve.Expo),
			Invert:             ac.Curve.Invert,
			Smoothing:          float32(ac.Smoothing),
			SignedThrottle:     ac.SignedThrottle,
			SignedThrottleIdle: float32(ac.SignedThrottleIdle),
		}
	}
	cfg := s.currentConfig()
	pkt := protocol.EncodeConfig(s.nextSeq(), protocol.Config{
		Pitch:    toWire(cfg.Pitch),
		Roll:     toWire(cfg.Roll),
		Throttle: toWire(cfg.Throttle),
		Rudder:   toWire(cfg.Rudder),
	})
	_, err := s.conn.WriteToUDP(pkt, addr)
	return err
}

func (s *Server) currentClient() *net.UDPAddr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientAddr
}

// isCurrentClient reports whether addr matches the address that most
// recently completed a HELLO handshake — AXIS/BUTTON packets are only
// applied to the backend from that established session, not from any
// device that happens to send a well-formed packet to this port. Without
// this, a second phone (or a stray/replayed packet) on the same network
// could apply control input alongside, or instead of, the connected pilot.
func (s *Server) isCurrentClient(addr *net.UDPAddr) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientAddr != nil && addr.String() == s.clientAddr.String()
}

func (s *Server) nextSeq() uint16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outSeq++
	return s.outSeq
}

func (s *Server) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			addr := s.currentClient()
			if addr == nil {
				continue
			}
			pkt := protocol.EncodeHeartbeat(s.nextSeq())
			if _, err := s.conn.WriteToUDP(pkt, addr); err != nil {
				s.log.Warn("failed to send heartbeat", "err", err)
			}
		}
	}
}

func (s *Server) failsafeLoop(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.Duration(s.cfg.FailsafeTimeoutMS) * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkFailsafe(timeout)
		}
	}
}

func (s *Server) checkFailsafe(timeout time.Duration) {
	s.mu.Lock()
	shouldTrigger := s.clientAddr != nil && !s.failsafeApplied && time.Since(s.lastAxisAt) >= timeout
	if shouldTrigger {
		s.failsafeApplied = true
	}
	s.mu.Unlock()

	if !shouldTrigger {
		return
	}

	s.log.Warn("AXIS timeout, applying failsafe", "timeout", timeout)
	s.applyFailsafe(control.AxisPitch, s.axisConfig(control.AxisPitch))
	s.applyFailsafe(control.AxisRoll, s.axisConfig(control.AxisRoll))
	s.applyFailsafe(control.AxisThrottle, s.axisConfig(control.AxisThrottle))
	s.applyFailsafe(control.AxisRudder, s.axisConfig(control.AxisRudder))
}

func (s *Server) applyFailsafe(id control.AxisID, ac config.AxisConfig) {
	if ac.Failsafe != config.FailsafeNeutral {
		return // hold: do nothing, the backend already retains its last value
	}
	// Reset smoothing state too, so the next real AXIS packet doesn't ramp
	// up from the stale pre-failsafe value instead of the neutral it's
	// actually starting from.
	s.mu.Lock()
	s.lastShaped[id] = 0
	s.mu.Unlock()
	if err := s.backend.SetAxis(id, 0); err != nil {
		s.warnBackendFailure("failsafe SetAxis failed", "axis", id, "err", err)
	}
}
