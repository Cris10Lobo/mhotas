// Package protocol is the Go side of the wire format in docs/protocol.md,
// hand-mirrored by app/lib/network/protocol.dart.
package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

// Version is the protocol version; a HELLO with a different one gets
// HelloVersionMismatch.
const Version uint8 = 8

// PacketType identifies the payload that follows the 4-byte Header.
type PacketType uint8

const (
	TypeAxis      PacketType = 0x01
	TypeButton    PacketType = 0x02
	TypeHello     PacketType = 0x03
	TypeHelloAck  PacketType = 0x04
	TypeHeartbeat PacketType = 0x05
	TypeTelemetry PacketType = 0x06
	TypeConfig    PacketType = 0x07
	TypeConfigSet PacketType = 0x08
	TypeConfigGet PacketType = 0x09
)

func (t PacketType) String() string {
	switch t {
	case TypeAxis:
		return "AXIS"
	case TypeButton:
		return "BUTTON"
	case TypeHello:
		return "HELLO"
	case TypeHelloAck:
		return "HELLO_ACK"
	case TypeHeartbeat:
		return "HEARTBEAT"
	case TypeTelemetry:
		return "TELEMETRY"
	case TypeConfig:
		return "CONFIG"
	case TypeConfigSet:
		return "CONFIG_SET"
	case TypeConfigGet:
		return "CONFIG_GET"
	default:
		return fmt.Sprintf("UNKNOWN(0x%02x)", uint8(t))
	}
}

const HeaderSize = 4

// NameFieldSize is the HELLO/HELLO_ACK name field width; the last byte stays
// 0 as a NUL terminator.
const NameFieldSize = 32

type Header struct {
	Type    PacketType
	Version uint8
	Seq     uint16
}

func DecodeHeader(b []byte) (Header, error) {
	if len(b) < HeaderSize {
		return Header{}, fmt.Errorf("protocol: packet too short for header: %d bytes", len(b))
	}
	return Header{
		Type:    PacketType(b[0]),
		Version: b[1],
		Seq:     binary.LittleEndian.Uint16(b[2:4]),
	}, nil
}

func encodeHeader(buf []byte, t PacketType, seq uint16) {
	buf[0] = byte(t)
	buf[1] = Version
	binary.LittleEndian.PutUint16(buf[2:4], seq)
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

func encodeName(dst []byte, name string) {
	b := []byte(name)
	if len(b) > NameFieldSize-1 {
		b = b[:NameFieldSize-1]
	}
	n := copy(dst, b)
	for i := n; i < NameFieldSize; i++ {
		dst[i] = 0
	}
}

func decodeName(src []byte) string {
	before, _, _ := bytes.Cut(src, []byte{0})
	return string(before)
}

// ---- AXIS (0x01) ----

const AxisPacketSize = HeaderSize + 16

// Axis is sent raw: dead zone and curve are applied server-side.
type Axis struct {
	Pitch    float32 // -1..1
	Roll     float32 // -1..1
	Throttle float32 // -1..1, negative = reverse thrust
	Rudder   float32 // -1..1
}

func EncodeAxis(seq uint16, a Axis) []byte {
	buf := make([]byte, AxisPacketSize)
	encodeHeader(buf, TypeAxis, seq)
	binary.LittleEndian.PutUint32(buf[4:8], math.Float32bits(a.Pitch))
	binary.LittleEndian.PutUint32(buf[8:12], math.Float32bits(a.Roll))
	binary.LittleEndian.PutUint32(buf[12:16], math.Float32bits(a.Throttle))
	binary.LittleEndian.PutUint32(buf[16:20], math.Float32bits(a.Rudder))
	return buf
}

func DecodeAxis(b []byte) (Axis, error) {
	if len(b) < AxisPacketSize {
		return Axis{}, fmt.Errorf("protocol: AXIS packet too short: %d bytes", len(b))
	}
	return Axis{
		Pitch:    math.Float32frombits(binary.LittleEndian.Uint32(b[4:8])),
		Roll:     math.Float32frombits(binary.LittleEndian.Uint32(b[8:12])),
		Throttle: math.Float32frombits(binary.LittleEndian.Uint32(b[12:16])),
		Rudder:   math.Float32frombits(binary.LittleEndian.Uint32(b[16:20])),
	}, nil
}

// ---- BUTTON (0x02) ----

const ButtonPacketSize = HeaderSize + 4

// Button is sent 3x per event against UDP loss; the receiver dedups by EventID.
type Button struct {
	ButtonID uint8
	Pressed  bool
	EventID  uint16
}

func EncodeButton(seq uint16, btn Button) []byte {
	buf := make([]byte, ButtonPacketSize)
	encodeHeader(buf, TypeButton, seq)
	buf[4] = btn.ButtonID
	if btn.Pressed {
		buf[5] = 1
	}
	binary.LittleEndian.PutUint16(buf[6:8], btn.EventID)
	return buf
}

func DecodeButton(b []byte) (Button, error) {
	if len(b) < ButtonPacketSize {
		return Button{}, fmt.Errorf("protocol: BUTTON packet too short: %d bytes", len(b))
	}
	return Button{
		ButtonID: b[4],
		Pressed:  b[5] != 0,
		EventID:  binary.LittleEndian.Uint16(b[6:8]),
	}, nil
}

// ---- HELLO (0x03) / HELLO_ACK (0x04) ----

const HelloPacketSize = HeaderSize + NameFieldSize

type Hello struct {
	DeviceName string
}

func EncodeHello(seq uint16, h Hello) []byte {
	buf := make([]byte, HelloPacketSize)
	encodeHeader(buf, TypeHello, seq)
	encodeName(buf[4:4+NameFieldSize], h.DeviceName)
	return buf
}

func DecodeHello(b []byte) (Hello, error) {
	if len(b) < HelloPacketSize {
		return Hello{}, fmt.Errorf("protocol: HELLO packet too short: %d bytes", len(b))
	}
	return Hello{DeviceName: decodeName(b[4 : 4+NameFieldSize])}, nil
}

type HelloAckStatus uint8

const (
	HelloOK              HelloAckStatus = 0
	HelloVersionMismatch HelloAckStatus = 1
)

const HelloAckPacketSize = HeaderSize + 1 + NameFieldSize

// HelloAck is also sent on version mismatch so the app can show a clear error.
type HelloAck struct {
	Status     HelloAckStatus
	ServerName string
}

func EncodeHelloAck(seq uint16, h HelloAck) []byte {
	buf := make([]byte, HelloAckPacketSize)
	encodeHeader(buf, TypeHelloAck, seq)
	buf[4] = byte(h.Status)
	encodeName(buf[5:5+NameFieldSize], h.ServerName)
	return buf
}

func DecodeHelloAck(b []byte) (HelloAck, error) {
	if len(b) < HelloAckPacketSize {
		return HelloAck{}, fmt.Errorf("protocol: HELLO_ACK packet too short: %d bytes", len(b))
	}
	return HelloAck{
		Status:     HelloAckStatus(b[4]),
		ServerName: decodeName(b[5 : 5+NameFieldSize]),
	}, nil
}

// ---- HEARTBEAT (0x05) ----

const HeartbeatPacketSize = HeaderSize

func EncodeHeartbeat(seq uint16) []byte {
	buf := make([]byte, HeartbeatPacketSize)
	encodeHeader(buf, TypeHeartbeat, seq)
	return buf
}

// ---- TELEMETRY (0x06) ----

type GearPosition uint8

const (
	GearUp      GearPosition = 0
	GearDown    GearPosition = 1
	GearTransit GearPosition = 2
)

const TelemetryPacketSize = HeaderSize + 22

// Bit flags packed into wire byte 13; bits 5-7 are unused.
const (
	telemetryFlagStallWarning       = 1 << 0
	telemetryFlagOnGround           = 1 << 1
	telemetryFlagSpoilersArmed      = 1 << 2
	telemetryFlagAutopilotMaster    = 1 << 3
	telemetryFlagAutothrottleActive = 1 << 4
)

// Telemetry is live sim state, so in-sim changes show on the phone instead
// of it holding stale local state.
type Telemetry struct {
	FlapHandleIndex    int8
	FlapHandleIndexMax int8
	GearPosition       GearPosition
	ParkingBrakeSet    bool
	ThrottlePercent    float32 // -1..1, negative = reverse
	Paused             bool
	StallWarning       bool
	OnGround           bool
	GroundSpeedKnots   float32
	SpoilersHandle     float32 // 0..1
	SpoilersArmed      bool
	ElevatorTrim       float32 // -1..1
	AutopilotMaster    bool
	AutothrottleActive bool
}

func EncodeTelemetry(seq uint16, t Telemetry) []byte {
	buf := make([]byte, TelemetryPacketSize)
	encodeHeader(buf, TypeTelemetry, seq)
	buf[4] = byte(t.FlapHandleIndex)
	buf[5] = byte(t.FlapHandleIndexMax)
	buf[6] = byte(t.GearPosition)
	buf[7] = boolByte(t.ParkingBrakeSet)
	binary.LittleEndian.PutUint32(buf[8:12], math.Float32bits(t.ThrottlePercent))
	buf[12] = boolByte(t.Paused)
	var flags byte
	if t.StallWarning {
		flags |= telemetryFlagStallWarning
	}
	if t.OnGround {
		flags |= telemetryFlagOnGround
	}
	if t.SpoilersArmed {
		flags |= telemetryFlagSpoilersArmed
	}
	if t.AutopilotMaster {
		flags |= telemetryFlagAutopilotMaster
	}
	if t.AutothrottleActive {
		flags |= telemetryFlagAutothrottleActive
	}
	buf[13] = flags
	binary.LittleEndian.PutUint32(buf[14:18], math.Float32bits(t.GroundSpeedKnots))
	binary.LittleEndian.PutUint32(buf[18:22], math.Float32bits(t.SpoilersHandle))
	binary.LittleEndian.PutUint32(buf[22:26], math.Float32bits(t.ElevatorTrim))
	return buf
}

func DecodeTelemetry(b []byte) (Telemetry, error) {
	if len(b) < TelemetryPacketSize {
		return Telemetry{}, fmt.Errorf("protocol: TELEMETRY packet too short: %d bytes", len(b))
	}
	flags := b[13]
	return Telemetry{
		FlapHandleIndex:    int8(b[4]),
		FlapHandleIndexMax: int8(b[5]),
		GearPosition:       GearPosition(b[6]),
		ParkingBrakeSet:    b[7] != 0,
		ThrottlePercent:    math.Float32frombits(binary.LittleEndian.Uint32(b[8:12])),
		Paused:             b[12] != 0,
		StallWarning:       flags&telemetryFlagStallWarning != 0,
		OnGround:           flags&telemetryFlagOnGround != 0,
		GroundSpeedKnots:   math.Float32frombits(binary.LittleEndian.Uint32(b[14:18])),
		SpoilersHandle:     math.Float32frombits(binary.LittleEndian.Uint32(b[18:22])),
		SpoilersArmed:      flags&telemetryFlagSpoilersArmed != 0,
		ElevatorTrim:       math.Float32frombits(binary.LittleEndian.Uint32(b[22:26])),
		AutopilotMaster:    flags&telemetryFlagAutopilotMaster != 0,
		AutothrottleActive: flags&telemetryFlagAutothrottleActive != 0,
	}, nil
}

// ---- CONFIG (0x07) / CONFIG_SET (0x08) ----

// ConfigAxis selects the axis a CONFIG_SET applies to. Failsafe is
// deliberately not phone-editable.
type ConfigAxis uint8

const (
	ConfigAxisPitch    ConfigAxis = 0
	ConfigAxisRoll     ConfigAxis = 1
	ConfigAxisThrottle ConfigAxis = 2
	ConfigAxisRudder   ConfigAxis = 3
)

// AxisCalibration is carried on every axis; the signed throttle fields only
// matter for throttle.
type AxisCalibration struct {
	DeadZone           float32
	Expo               float32
	Invert             bool
	Smoothing          float32
	SignedThrottle     bool
	SignedThrottleIdle float32
}

const axisCalibrationSize = 18

func encodeAxisCalibration(buf []byte, ac AxisCalibration) {
	binary.LittleEndian.PutUint32(buf[0:4], math.Float32bits(ac.DeadZone))
	binary.LittleEndian.PutUint32(buf[4:8], math.Float32bits(ac.Expo))
	buf[8] = boolByte(ac.Invert)
	binary.LittleEndian.PutUint32(buf[9:13], math.Float32bits(ac.Smoothing))
	buf[13] = boolByte(ac.SignedThrottle)
	binary.LittleEndian.PutUint32(buf[14:18], math.Float32bits(ac.SignedThrottleIdle))
}

func decodeAxisCalibration(b []byte) AxisCalibration {
	return AxisCalibration{
		DeadZone:           math.Float32frombits(binary.LittleEndian.Uint32(b[0:4])),
		Expo:               math.Float32frombits(binary.LittleEndian.Uint32(b[4:8])),
		Invert:             b[8] != 0,
		Smoothing:          math.Float32frombits(binary.LittleEndian.Uint32(b[9:13])),
		SignedThrottle:     b[13] != 0,
		SignedThrottleIdle: math.Float32frombits(binary.LittleEndian.Uint32(b[14:18])),
	}
}

const ConfigPacketSize = HeaderSize + 4*axisCalibrationSize

// Config is a full snapshot of every axis, not a diff.
type Config struct {
	Pitch, Roll, Throttle, Rudder AxisCalibration
}

func EncodeConfig(seq uint16, c Config) []byte {
	buf := make([]byte, ConfigPacketSize)
	encodeHeader(buf, TypeConfig, seq)
	encodeAxisCalibration(buf[4+0*axisCalibrationSize:], c.Pitch)
	encodeAxisCalibration(buf[4+1*axisCalibrationSize:], c.Roll)
	encodeAxisCalibration(buf[4+2*axisCalibrationSize:], c.Throttle)
	encodeAxisCalibration(buf[4+3*axisCalibrationSize:], c.Rudder)
	return buf
}

func DecodeConfig(b []byte) (Config, error) {
	if len(b) < ConfigPacketSize {
		return Config{}, fmt.Errorf("protocol: CONFIG packet too short: %d bytes", len(b))
	}
	return Config{
		Pitch:    decodeAxisCalibration(b[4+0*axisCalibrationSize:]),
		Roll:     decodeAxisCalibration(b[4+1*axisCalibrationSize:]),
		Throttle: decodeAxisCalibration(b[4+2*axisCalibrationSize:]),
		Rudder:   decodeAxisCalibration(b[4+3*axisCalibrationSize:]),
	}, nil
}

const ConfigGetPacketSize = HeaderSize

func EncodeConfigGet(seq uint16) []byte {
	buf := make([]byte, ConfigGetPacketSize)
	encodeHeader(buf, TypeConfigGet, seq)
	return buf
}

const ConfigSetPacketSize = HeaderSize + 1 + axisCalibrationSize

// ConfigSet is acked by a fresh CONFIG, not a dedicated packet.
type ConfigSet struct {
	Axis        ConfigAxis
	Calibration AxisCalibration
}

func EncodeConfigSet(seq uint16, cs ConfigSet) []byte {
	buf := make([]byte, ConfigSetPacketSize)
	encodeHeader(buf, TypeConfigSet, seq)
	buf[4] = byte(cs.Axis)
	encodeAxisCalibration(buf[5:], cs.Calibration)
	return buf
}

func DecodeConfigSet(b []byte) (ConfigSet, error) {
	if len(b) < ConfigSetPacketSize {
		return ConfigSet{}, fmt.Errorf("protocol: CONFIG_SET packet too short: %d bytes", len(b))
	}
	return ConfigSet{
		Axis:        ConfigAxis(b[4]),
		Calibration: decodeAxisCalibration(b[5:]),
	}, nil
}
