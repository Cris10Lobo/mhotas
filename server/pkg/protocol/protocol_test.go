package protocol

import (
	"strings"
	"testing"
)

func TestAxisRoundTrip(t *testing.T) {
	want := Axis{Pitch: -0.5, Roll: 0.75, Throttle: 1.0, Rudder: -1.0}
	buf := EncodeAxis(42, want)
	if len(buf) != AxisPacketSize {
		t.Fatalf("EncodeAxis: got %d bytes, want %d", len(buf), AxisPacketSize)
	}
	hdr, err := DecodeHeader(buf)
	if err != nil {
		t.Fatalf("DecodeHeader: %v", err)
	}
	if hdr.Type != TypeAxis || hdr.Version != Version || hdr.Seq != 42 {
		t.Fatalf("header mismatch: %+v", hdr)
	}
	got, err := DecodeAxis(buf)
	if err != nil {
		t.Fatalf("DecodeAxis: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestButtonRoundTrip(t *testing.T) {
	want := Button{ButtonID: 3, Pressed: true, EventID: 65535}
	buf := EncodeButton(1, want)
	got, err := DecodeButton(buf)
	if err != nil {
		t.Fatalf("DecodeButton: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestHelloRoundTrip(t *testing.T) {
	want := Hello{DeviceName: "cris-iphone"}
	buf := EncodeHello(0, want)
	got, err := DecodeHello(buf)
	if err != nil {
		t.Fatalf("DecodeHello: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestHelloNameTruncation(t *testing.T) {
	longName := strings.Repeat("x", 100)
	buf := EncodeHello(0, Hello{DeviceName: longName})
	got, err := DecodeHello(buf)
	if err != nil {
		t.Fatalf("DecodeHello: %v", err)
	}
	if len(got.DeviceName) != NameFieldSize-1 {
		t.Fatalf("expected truncation to %d bytes, got %d", NameFieldSize-1, len(got.DeviceName))
	}
}

func TestHelloAckRoundTrip(t *testing.T) {
	want := HelloAck{Status: HelloVersionMismatch, ServerName: "cris-pc"}
	buf := EncodeHelloAck(7, want)
	got, err := DecodeHelloAck(buf)
	if err != nil {
		t.Fatalf("DecodeHelloAck: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestHeartbeatHeaderOnly(t *testing.T) {
	buf := EncodeHeartbeat(9)
	if len(buf) != HeartbeatPacketSize {
		t.Fatalf("EncodeHeartbeat: got %d bytes, want %d", len(buf), HeartbeatPacketSize)
	}
	hdr, err := DecodeHeader(buf)
	if err != nil {
		t.Fatalf("DecodeHeader: %v", err)
	}
	if hdr.Type != TypeHeartbeat || hdr.Seq != 9 {
		t.Fatalf("header mismatch: %+v", hdr)
	}
}

func TestTelemetryRoundTrip(t *testing.T) {
	want := Telemetry{
		FlapHandleIndex:    2,
		FlapHandleIndexMax: 4,
		GearPosition:       GearDown,
		ParkingBrakeSet:    true,
		ThrottlePercent:    -0.5,
		Paused:             true,
		StallWarning:       true,
		OnGround:           true,
		GroundSpeedKnots:   12.5,
		SpoilersHandle:     0.75,
		SpoilersArmed:      true,
		ElevatorTrim:       -0.25,
		AutopilotMaster:    true,
		AutothrottleActive: true,
	}
	buf := EncodeTelemetry(3, want)
	got, err := DecodeTelemetry(buf)
	if err != nil {
		t.Fatalf("DecodeTelemetry: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestConfigGetHeaderOnly(t *testing.T) {
	buf := EncodeConfigGet(4)
	if len(buf) != ConfigGetPacketSize {
		t.Fatalf("EncodeConfigGet: got %d bytes, want %d", len(buf), ConfigGetPacketSize)
	}
	hdr, err := DecodeHeader(buf)
	if err != nil {
		t.Fatalf("DecodeHeader: %v", err)
	}
	if hdr.Type != TypeConfigGet || hdr.Seq != 4 {
		t.Fatalf("header mismatch: %+v", hdr)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	ac := func(dz, expo, smoothing float32, invert bool) AxisCalibration {
		return AxisCalibration{DeadZone: dz, Expo: expo, Invert: invert, Smoothing: smoothing}
	}
	want := Config{
		Pitch:    ac(0.05, 0.6, 0.5, false),
		Roll:     ac(0.05, 0.6, 0.5, true),
		Throttle: ac(0.05, 0, 0, false),
		Rudder:   ac(0, 0, 0, true),
	}
	buf := EncodeConfig(1, want)
	if len(buf) != ConfigPacketSize {
		t.Fatalf("EncodeConfig: got %d bytes, want %d", len(buf), ConfigPacketSize)
	}
	got, err := DecodeConfig(buf)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestConfigSetRoundTrip(t *testing.T) {
	want := ConfigSet{
		Axis:        ConfigAxisThrottle,
		Calibration: AxisCalibration{DeadZone: 0.1, Expo: 0.3, Invert: true, Smoothing: 0.4},
	}
	buf := EncodeConfigSet(5, want)
	if len(buf) != ConfigSetPacketSize {
		t.Fatalf("EncodeConfigSet: got %d bytes, want %d", len(buf), ConfigSetPacketSize)
	}
	got, err := DecodeConfigSet(buf)
	if err != nil {
		t.Fatalf("DecodeConfigSet: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestDecodeTooShort(t *testing.T) {
	cases := []struct {
		name string
		fn   func([]byte) error
	}{
		{"header", func(b []byte) error { _, err := DecodeHeader(b); return err }},
		{"axis", func(b []byte) error { _, err := DecodeAxis(b); return err }},
		{"button", func(b []byte) error { _, err := DecodeButton(b); return err }},
		{"hello", func(b []byte) error { _, err := DecodeHello(b); return err }},
		{"helloAck", func(b []byte) error { _, err := DecodeHelloAck(b); return err }},
		{"telemetry", func(b []byte) error { _, err := DecodeTelemetry(b); return err }},
		{"config", func(b []byte) error { _, err := DecodeConfig(b); return err }},
		{"configSet", func(b []byte) error { _, err := DecodeConfigSet(b); return err }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.fn([]byte{0x01, 0x01}); err == nil {
				t.Fatalf("expected error for short packet")
			}
		})
	}
}
