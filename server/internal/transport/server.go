// Package transport owns the UDP socket: it turns incoming phone packets
// into calibrated control.Output calls, tracks the connected phone's
// address for pushing TELEMETRY/HEARTBEAT back, and runs the failsafe
// timer described in docs/protocol.md.
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

	"mhotas/internal/config"
	"mhotas/internal/control"
	"mhotas/pkg/protocol"
)

// Server reads phone packets, drives a control.Output backend, and pushes
// telemetry/heartbeats back to whichever phone last sent a valid HELLO.
type Server struct {
	conn    *net.UDPConn
	backend control.Output
	cfg     config.Config
	log     *slog.Logger

	mu              sync.Mutex
	clientAddr      *net.UDPAddr
	lastAxisAt      time.Time
	failsafeApplied bool
	lastButtonEvent map[uint8]uint16
	lastShaped      map[control.AxisID]float64
	outSeq          uint16
}

// New binds a UDP socket on cfg.ListenPort and returns a Server driving backend.
func New(cfg config.Config, backend control.Output, log *slog.Logger) (*Server, error) {
	// "udp4", not "udp": phones connect via manual IPv4 entry, and an
	// unspecified bind on Windows can land on the IPv6 wildcard, which
	// WSL's IPv4 loopback forwarding can't reach ("connection refused").
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: cfg.ListenPort})
	if err != nil {
		return nil, fmt.Errorf("transport: listen on port %d: %w", cfg.ListenPort, err)
	}
	return &Server{
		conn:            conn,
		backend:         backend,
		cfg:             cfg,
		log:             log,
		lastButtonEvent: make(map[uint8]uint16),
		lastShaped:      make(map[control.AxisID]float64),
	}, nil
}

// Addr returns the UDP address the server is listening on.
func (s *Server) Addr() *net.UDPAddr {
	return s.conn.LocalAddr().(*net.UDPAddr)
}

// Run reads packets and drives the failsafe/heartbeat timers until ctx is
// canceled, then closes the socket.
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

	s.applyAxis(control.AxisPitch, float64(axis.Pitch), s.cfg.Pitch)
	s.applyAxis(control.AxisRoll, float64(axis.Roll), s.cfg.Roll)
	s.applyAxis(control.AxisThrottle, float64(axis.Throttle), s.cfg.Throttle)
	s.applyAxis(control.AxisRudder, float64(axis.Rudder), s.cfg.Rudder)
}

func (s *Server) applyAxis(id control.AxisID, raw float64, ac config.AxisConfig) {
	shaped, changed := s.smooth(id, ac.Curve.Apply(raw), ac.Smoothing)
	if !changed {
		// Value hasn't moved since the last packet — don't re-assert it.
		// Without this, mhotas would resend the same throttle/rudder value
		// to the backend 30-50x/sec regardless of what the sim itself is
		// now showing, fighting any adjustment made directly in the sim
		// (dragging the throttle with a mouse, autothrottle, etc.) within
		// one packet interval. Pitch/roll are unaffected in practice —
		// sensor noise means they essentially never repeat bit-for-bit.
		// See docs/protocol.md's "Bidirectional axis sync".
		return
	}
	if err := s.backend.SetAxis(id, shaped); err != nil {
		s.log.Warn("backend SetAxis failed", "axis", id, "err", err)
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
		s.log.Warn("backend SetButton failed", "button", btn.ButtonID, "err", err)
		return
	}
	// Buttons are rare, discrete events (unlike the 30Hz AXIS stream), so
	// logging every one isn't spam — it's the only way to see a press
	// actually reached and succeeded, since backends stay silent on success.
	s.log.Info("button", "id", btn.ButtonID, "pressed", btn.Pressed)
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
	s.applyFailsafe(control.AxisPitch, s.cfg.Pitch)
	s.applyFailsafe(control.AxisRoll, s.cfg.Roll)
	s.applyFailsafe(control.AxisThrottle, s.cfg.Throttle)
	s.applyFailsafe(control.AxisRudder, s.cfg.Rudder)
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
		s.log.Warn("failsafe SetAxis failed", "axis", id, "err", err)
	}
}
