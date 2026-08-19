// Dart mirror of the mHOTAS wire protocol defined in docs/protocol.md.
// Both the Flutter app and the Go server hand-implement against that
// document — there is no shared codegen. If you change a packet layout,
// update docs/protocol.md and server/pkg/protocol/protocol.go in the same
// change.
library;

import 'dart:convert';
import 'dart:typed_data';

class ProtocolVersion {
  static const int current = 1;
}

class PacketType {
  static const int axis = 0x01;
  static const int button = 0x02;
  static const int hello = 0x03;
  static const int helloAck = 0x04;
  static const int heartbeat = 0x05;
  static const int telemetry = 0x06;
}

const int headerSize = 4;
const int nameFieldSize = 32;

class Header {
  final int type;
  final int version;
  final int seq;

  const Header({required this.type, required this.version, required this.seq});

  static Header decode(Uint8List b) {
    if (b.length < headerSize) {
      throw FormatException('packet too short for header: ${b.length} bytes');
    }
    final bd = ByteData.sublistView(b);
    return Header(
      type: b[0],
      version: b[1],
      seq: bd.getUint16(2, Endian.little),
    );
  }
}

Uint8List _newPacket(int type, int seq, int totalSize) {
  final buf = Uint8List(totalSize);
  buf[0] = type;
  buf[1] = ProtocolVersion.current;
  ByteData.sublistView(buf).setUint16(2, seq, Endian.little);
  return buf;
}

Uint8List _encodeName(String name) {
  final bytes = utf8.encode(name);
  final field = Uint8List(nameFieldSize);
  final n = bytes.length > nameFieldSize - 1 ? nameFieldSize - 1 : bytes.length;
  field.setRange(0, n, bytes);
  return field;
}

String _decodeName(Uint8List field) {
  final end = field.indexOf(0);
  final bytes = end >= 0 ? field.sublist(0, end) : field;
  return utf8.decode(bytes);
}

// ---- AXIS (0x01) ----

const int axisPacketSize = headerSize + 16;

/// Continuous stick/throttle/rudder position. No dead-zone or curve is
/// applied before sending — that's server-side (see docs/protocol.md) —
/// but the recenter/zero offset (see AttitudeStream) is applied before
/// normalizing, since that corrects for physical grip, not sim response.
class Axis {
  final double pitch; // -1..1
  final double roll; // -1..1
  final double throttle; // -1..1, negative = reverse thrust
  final double rudder; // -1..1

  const Axis({
    required this.pitch,
    required this.roll,
    required this.throttle,
    required this.rudder,
  });
}

Uint8List encodeAxis(int seq, Axis a) {
  final buf = _newPacket(PacketType.axis, seq, axisPacketSize);
  final bd = ByteData.sublistView(buf);
  bd.setFloat32(4, a.pitch, Endian.little);
  bd.setFloat32(8, a.roll, Endian.little);
  bd.setFloat32(12, a.throttle, Endian.little);
  bd.setFloat32(16, a.rudder, Endian.little);
  return buf;
}

Axis decodeAxis(Uint8List b) {
  if (b.length < axisPacketSize) {
    throw FormatException('AXIS packet too short: ${b.length} bytes');
  }
  final bd = ByteData.sublistView(b);
  return Axis(
    pitch: bd.getFloat32(4, Endian.little),
    roll: bd.getFloat32(8, Endian.little),
    throttle: bd.getFloat32(12, Endian.little),
    rudder: bd.getFloat32(16, Endian.little),
  );
}

// ---- BUTTON (0x02) ----

const int buttonPacketSize = headerSize + 4;

/// A single discrete button press/release event. The app sends each event
/// 3x in quick succession so a dropped UDP packet doesn't lose it — see
/// UdpClient.sendButton — and the server de-dupes by eventId.
class Button {
  final int buttonId;
  final bool pressed;
  final int eventId;

  const Button({required this.buttonId, required this.pressed, required this.eventId});
}

Uint8List encodeButton(int seq, Button btn) {
  final buf = _newPacket(PacketType.button, seq, buttonPacketSize);
  final bd = ByteData.sublistView(buf);
  buf[4] = btn.buttonId & 0xff;
  buf[5] = btn.pressed ? 1 : 0;
  bd.setUint16(6, btn.eventId, Endian.little);
  return buf;
}

Button decodeButton(Uint8List b) {
  if (b.length < buttonPacketSize) {
    throw FormatException('BUTTON packet too short: ${b.length} bytes');
  }
  final bd = ByteData.sublistView(b);
  return Button(
    buttonId: b[4],
    pressed: b[5] != 0,
    eventId: bd.getUint16(6, Endian.little),
  );
}

// ---- HELLO (0x03) / HELLO_ACK (0x04) ----

const int helloPacketSize = headerSize + nameFieldSize;

class Hello {
  final String deviceName;
  const Hello({required this.deviceName});
}

Uint8List encodeHello(int seq, Hello h) {
  final buf = _newPacket(PacketType.hello, seq, helloPacketSize);
  buf.setRange(4, 4 + nameFieldSize, _encodeName(h.deviceName));
  return buf;
}

Hello decodeHello(Uint8List b) {
  if (b.length < helloPacketSize) {
    throw FormatException('HELLO packet too short: ${b.length} bytes');
  }
  return Hello(deviceName: _decodeName(b.sublist(4, 4 + nameFieldSize)));
}

class HelloAckStatus {
  static const int ok = 0;
  static const int versionMismatch = 1;
}

const int helloAckPacketSize = headerSize + 1 + nameFieldSize;

class HelloAck {
  final int status;
  final String serverName;
  const HelloAck({required this.status, required this.serverName});
}

HelloAck decodeHelloAck(Uint8List b) {
  if (b.length < helloAckPacketSize) {
    throw FormatException('HELLO_ACK packet too short: ${b.length} bytes');
  }
  return HelloAck(
    status: b[4],
    serverName: _decodeName(b.sublist(5, 5 + nameFieldSize)),
  );
}

// ---- HEARTBEAT (0x05) ----

const int heartbeatPacketSize = headerSize;

Uint8List encodeHeartbeat(int seq) => _newPacket(PacketType.heartbeat, seq, heartbeatPacketSize);

// ---- TELEMETRY (0x06) ----

class GearPosition {
  static const int up = 0;
  static const int down = 1;
  static const int transit = 2;
}

const int telemetryPacketSize = headerSize + 10;

/// Live aircraft state read from SimConnect, pushed server → phone. See
/// docs/protocol.md — v1 intentionally ships this fixed small struct
/// rather than a generic key/value simvar schema.
///
/// Every field here exists to close a "double lane" gap for something the
/// phone can also command: an in-sim change (mouse, keyboard, autothrottle,
/// the Esc key, another control) should show up here instead of the app
/// silently holding a stale value or guessing state locally.
class Telemetry {
  final int flapHandleIndex;
  final int flapHandleIndexMax;
  final int gearPosition;

  /// The sim's actual current parking brake state, independent of this
  /// app's own button-tap history.
  final bool parkingBrakeSet;

  /// The sim's actual current throttle lever position (-1..1, negative =
  /// reverse), independent of whatever the phone last commanded. Used to
  /// mirror the throttle lever widget while the pilot isn't touching it,
  /// instead of silently holding a stale value.
  final double throttlePercent;

  /// The sim's actual current pause state, from SimConnect's own Pause
  /// system event — not a guess based on this app's last PAUSE tap, which
  /// can't see an external pause/unpause.
  final bool paused;

  const Telemetry({
    required this.flapHandleIndex,
    required this.flapHandleIndexMax,
    required this.gearPosition,
    required this.parkingBrakeSet,
    required this.throttlePercent,
    required this.paused,
  });
}

Telemetry decodeTelemetry(Uint8List b) {
  if (b.length < telemetryPacketSize) {
    throw FormatException('TELEMETRY packet too short: ${b.length} bytes');
  }
  final bd = ByteData.sublistView(b);
  return Telemetry(
    flapHandleIndex: b[4].toSigned(8),
    flapHandleIndexMax: b[5].toSigned(8),
    gearPosition: b[6],
    parkingBrakeSet: b[7] != 0,
    throttlePercent: bd.getFloat32(8, Endian.little),
    paused: b[12] != 0,
  );
}
