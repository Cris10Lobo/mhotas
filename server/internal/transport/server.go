// Package transport turns phone UDP packets into calibrated control.Output
// calls and pushes telemetry back. Only one phone is served at a time.
package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"strconv"
	"sync"
	"time"

	"mhotas/internal/calibration"
	"mhotas/internal/config"
	"mhotas/internal/control"
	"mhotas/pkg/protocol"
)

type Server struct {
	conn       *net.UDPConn
	backend    control.Output
	log        *slog.Logger
	configPath string // "" = CONFIG_SET isn't persisted

	// cfg is written by CONFIG_SET and read by failsafeLoop; access it only
	// through the mu-guarded helpers below.
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

// deviceLivenessTimeout mirrors the app's UdpClient liveness check.
const deviceLivenessTimeout = 3 * time.Second

// Status is a display snapshot for the TUI.
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

// New starts listening on cfg.ListenPort. configPath is where CONFIG_SET
// persists changes ("" to skip).
func New(cfg config.Config, backend control.Output, log *slog.Logger, configPath string) (*Server, error) {
	// "udp4": an unspecified bind on Windows can land on the IPv6 wildcard,
	// which WSL's IPv4 loopback forwarding can't reach.
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

// curve drops throttle invert when the backend applies it after its idle
// remap (control.SignedThrottleSetter); inverting here would swap the
// forward and reverse zones around idle.
func (s *Server) curve(id control.AxisID, ac config.AxisConfig) calibration.Curve {
	c := ac.Curve
	if _, ok := s.backend.(control.SignedThrottleSetter); ok && id == control.AxisThrottle {
		c.Invert = false
	}
	return c
}

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

func (s *Server) currentConfig() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// setAxisConfig replaces one axis's calibration but keeps its Failsafe,
// which is config-file-only.
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

// safeHandlePacket keeps one bad packet from killing the server mid-flight.
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
	// No calibration and no failsafe: a lever position, held like throttle.
	s.applyAxis(control.AxisSpeedbrake, clamp01(float64(axis.Speedbrake)), config.AxisConfig{})
}

func (s *Server) applyAxis(id control.AxisID, raw float64, ac config.AxisConfig) {
	shaped, changed := s.smooth(id, s.curve(id, ac).Apply(raw), ac.Smoothing)
	if !changed {
		// Re-sending an unchanged value would fight in-sim adjustments
		// (mouse-dragged throttle, autothrottle) within one packet interval.
		return
	}
	if err := s.backend.SetAxis(id, shaped); err != nil {
		s.warnBackendFailure("backend SetAxis failed", "axis", id, "err", err)
	}
}

// backendWarnInterval rate-limits backend failure logs; AXIS arrives 30-50x/sec.
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

// changeEpsilon: an EMA only approaches its target asymptotically, so exact
// equality would keep reporting changes long after the input stopped moving.
const changeEpsilon = 1e-6

// smooth is an EMA; changed reports whether out moved beyond changeEpsilon.
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
	// int16 difference handles uint16 wraparound.
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
	s.log.Info("button", "id", btn.ButtonID, "pressed", btn.Pressed)
}

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
	// Clamp at the trust boundary; Curve.Apply only guards DeadZone>=1.
	cal := cs.Calibration
	ac := config.AxisConfig{
		Curve: calibration.Curve{
			DeadZone: clamp01(widen(cal.DeadZone)),
			Expo:     clamp01(widen(cal.Expo)),
			Invert:   cal.Invert,
		},
		Smoothing:          clamp01(widen(cal.Smoothing)),
		SignedThrottle:     cal.SignedThrottle,
		SignedThrottleIdle: clampSigned(widen(cal.SignedThrottleIdle)),
	}
	id, ok := s.setAxisConfig(cs.Axis, ac)
	if !ok {
		s.log.Warn("bad CONFIG_SET axis", "axis", cs.Axis, "from", addr)
		return
	}
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

func (s *Server) handleConfigGet(addr *net.UDPAddr) {
	if !s.isCurrentClient(addr) {
		s.log.Warn("dropping CONFIG_GET from non-established address", "from", addr)
		return
	}
	if err := s.SendConfig(); err != nil {
		s.log.Warn("failed to send CONFIG", "err", err)
	}
}

// widen converts a wire float32 to float64 via its shortest decimal form, so
// 0.05 is saved to config.json as 0.05 rather than 0.05000000074505806.
func widen(f float32) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(float64(f), 'g', -1, 32), 64)
	return v
}

func clamp01(v float64) float64 { return clampRange(v, 0, 1) }

func clampSigned(v float64) float64 { return clampRange(v, -1, 1) }

func clampRange(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return max(lo, min(hi, v))
}

// SendTelemetry is a no-op with no phone connected. ThrottlePercent is the
// throttle curve's output, so it is mapped back through the curve's inverse;
// otherwise a nonzero expo/dead zone made the phone's lever jump on every sync.
func (s *Server) SendTelemetry(t protocol.Telemetry) error {
	addr := s.currentClient()
	if addr == nil {
		return nil
	}
	t.ThrottlePercent = float32(s.curve(control.AxisThrottle, s.axisConfig(control.AxisThrottle)).Inverse(float64(t.ThrottlePercent)))
	pkt := protocol.EncodeTelemetry(s.nextSeq(), t)
	_, err := s.conn.WriteToUDP(pkt, addr)
	return err
}

// SendConfig pushes the current calibration to the connected phone, if any.
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

// isCurrentClient keeps a second phone or stray packet on the network from
// injecting control input alongside the connected pilot.
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
		return
	}
	// Reset smoothing so the next AXIS doesn't ramp from the stale value.
	s.mu.Lock()
	s.lastShaped[id] = 0
	s.mu.Unlock()
	if err := s.backend.SetAxis(id, 0); err != nil {
		s.warnBackendFailure("failsafe SetAxis failed", "axis", id, "err", err)
	}
}
