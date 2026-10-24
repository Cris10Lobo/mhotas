// Command udp-fake-phone sends synthetic AXIS/BUTTON packets to mhotas-server and logs what comes back.
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"time"

	"mhotas/pkg/protocol"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "udp-fake-phone:", err)
		os.Exit(1)
	}
}

func run() error {
	target := flag.String("target", "127.0.0.1:9750", "mhotas-server address")
	device := flag.String("device", "udp-fake-phone", "device name sent in HELLO")
	rate := flag.Float64("rate", 30, "AXIS packets per second")
	flag.Parse()

	addr, err := net.ResolveUDPAddr("udp", *target)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", *target, err)
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return fmt.Errorf("dialing %s: %w", *target, err)
	}
	defer conn.Close()

	go readLoop(conn)

	if err := sayHello(conn, *device); err != nil {
		return err
	}

	sendAxisSweep(conn, *rate)
	return nil
}

func sayHello(conn *net.UDPConn, device string) error {
	pkt := protocol.EncodeHello(1, protocol.Hello{DeviceName: device})
	if _, err := conn.Write(pkt); err != nil {
		return fmt.Errorf("sending HELLO: %w", err)
	}
	log.Printf("sent HELLO as %q, listening for HELLO_ACK...", device)
	return nil
}

// sendAxisSweep oscillates the axes and toggles a button every 5s, sent 3x like the real app.
func sendAxisSweep(conn *net.UDPConn, rate float64) {
	interval := time.Duration(float64(time.Second) / rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var seq, buttonEventID uint16
	start := time.Now()
	lastButtonAt := start
	buttonPressed := false

	log.Printf("sending AXIS packets at %.0fHz (Ctrl+C to stop)...", rate)
	for range ticker.C {
		t := time.Since(start).Seconds()
		axis := protocol.Axis{
			Pitch:    float32(math.Sin(t * 0.5)),
			Roll:     float32(math.Sin(t*0.3) * 0.7),
			Throttle: float32((math.Sin(t*0.2) + 1) / 2),
			Rudder:   float32(math.Sin(t*0.4) * 0.5),
		}
		seq++
		if _, err := conn.Write(protocol.EncodeAxis(seq, axis)); err != nil {
			log.Printf("write AXIS failed: %v", err)
		}

		if time.Since(lastButtonAt) >= 5*time.Second {
			lastButtonAt = time.Now()
			buttonPressed = !buttonPressed
			buttonEventID++
			btn := protocol.Button{ButtonID: 1, Pressed: buttonPressed, EventID: buttonEventID}
			for range 3 {
				seq++
				if _, err := conn.Write(protocol.EncodeButton(seq, btn)); err != nil {
					log.Printf("write BUTTON failed: %v", err)
				}
			}
			log.Printf("sent BUTTON id=1 pressed=%v (eventID=%d)", buttonPressed, buttonEventID)
		}
	}
}

func readLoop(conn *net.UDPConn) {
	buf := make([]byte, 1500)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		logPacket(buf[:n])
	}
}

func logPacket(b []byte) {
	hdr, err := protocol.DecodeHeader(b)
	if err != nil {
		log.Printf("received malformed packet: %v", err)
		return
	}
	switch hdr.Type {
	case protocol.TypeHelloAck:
		ack, err := protocol.DecodeHelloAck(b)
		if err != nil {
			log.Printf("received malformed HELLO_ACK: %v", err)
			return
		}
		log.Printf("received HELLO_ACK from %q, status=%v", ack.ServerName, ack.Status)
	case protocol.TypeHeartbeat:
		log.Printf("received HEARTBEAT (seq=%d)", hdr.Seq)
	case protocol.TypeTelemetry:
		tel, err := protocol.DecodeTelemetry(b)
		if err != nil {
			log.Printf("received malformed TELEMETRY: %v", err)
			return
		}
		log.Printf("received TELEMETRY flap=%d/%d gear=%v brake=%v throttle=%.2f paused=%v",
			tel.FlapHandleIndex, tel.FlapHandleIndexMax, tel.GearPosition,
			tel.ParkingBrakeSet, tel.ThrottlePercent, tel.Paused)
	default:
		log.Printf("received unexpected packet type %v from server", hdr.Type)
	}
}
