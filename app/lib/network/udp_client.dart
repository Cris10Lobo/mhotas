import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'protocol.dart' as proto;

enum ConnectionStatus {
  disconnected,
  connecting,
  connected,
  versionMismatch,
  error,
}

/// Talks the mHOTAS UDP protocol to a running mhotas-server. One socket
/// handles both directions once HELLO_ACK establishes the session.
class UdpClient {
  UdpClient({required this.deviceName});

  final String deviceName;

  RawDatagramSocket? _socket;
  InternetAddress? _serverAddress;
  int? _serverPort;
  int _seq = 0;

  final _statusController = StreamController<ConnectionStatus>.broadcast();
  final _telemetryController = StreamController<proto.Telemetry>.broadcast();
  ConnectionStatus _lastStatus = ConnectionStatus.disconnected;

  // The server sends HEARTBEAT/TELEMETRY at ~1Hz (docs/protocol.md), so
  // silence past a few periods means the link is actually down (WiFi drop,
  // backgrounding) rather than just a slow tick. Checked once a second;
  // while down, the same tick resends HELLO so the app reconnects on its
  // own once the link returns instead of requiring a manual reconnect.
  Timer? _livenessTimer;
  DateTime _lastRxAt = DateTime.fromMillisecondsSinceEpoch(0);
  static const _livenessTimeout = Duration(seconds: 3);

  Stream<ConnectionStatus> get status => _statusController.stream;
  Stream<proto.Telemetry> get telemetry => _telemetryController.stream;

  void _setStatus(ConnectionStatus s) {
    _lastStatus = s;
    _statusController.add(s);
  }

  /// Binds a local socket (if not already bound) and sends HELLO to
  /// host:port. Connection isn't confirmed until a HELLO_ACK arrives on
  /// [status].
  Future<void> connect(String host, int port) async {
    _setStatus(ConnectionStatus.connecting);
    try {
      _serverAddress = InternetAddress(host);
    } on FormatException {
      _setStatus(ConnectionStatus.error);
      return;
    }
    _serverPort = port;

    // Only bind+listen once — RawDatagramSocket's stream is single-
    // subscription, so calling .listen() again on a retry (e.g. after a
    // version-mismatch or resolve error, which don't call disconnect())
    // would throw "Stream has already been listened to".
    if (_socket == null) {
      _socket = await RawDatagramSocket.bind(InternetAddress.anyIPv4, 0);
      _socket!.listen(_onSocketEvent);
    }

    _lastRxAt = DateTime.now();
    _livenessTimer?.cancel();
    _livenessTimer =
        Timer.periodic(const Duration(seconds: 1), (_) => _checkLiveness());

    _send(proto.encodeHello(_nextSeq(), proto.Hello(deviceName: deviceName)));
  }

  void _checkLiveness() {
    if (DateTime.now().difference(_lastRxAt) <= _livenessTimeout) return;
    if (_lastStatus == ConnectionStatus.connected) {
      _setStatus(ConnectionStatus.disconnected);
    }
    _send(proto.encodeHello(_nextSeq(), proto.Hello(deviceName: deviceName)));
  }

  void _onSocketEvent(RawSocketEvent event) {
    if (event != RawSocketEvent.read) return;
    final datagram = _socket?.receive();
    if (datagram == null) return;
    _lastRxAt = DateTime.now();
    _handlePacket(datagram.data);
  }

  void _handlePacket(Uint8List data) {
    final proto.Header header;
    try {
      header = proto.Header.decode(data);
    } on FormatException {
      return;
    }

    // The per-packet decodeX calls below can also throw FormatException
    // (a truncated body — a reassembled/short UDP datagram, or a server
    // sending a shorter payload than this build expects) — guard the
    // whole switch, not just the header, so a single bad packet doesn't
    // throw uncaught inside this socket listener callback.
    try {
      _handleDecodedPacket(header, data);
    } on FormatException {
      return;
    }
  }

  void _handleDecodedPacket(proto.Header header, Uint8List data) {
    switch (header.type) {
      case proto.PacketType.helloAck:
        final ack = proto.decodeHelloAck(data);
        _setStatus(ack.status == proto.HelloAckStatus.ok
            ? ConnectionStatus.connected
            : ConnectionStatus.versionMismatch);
      case proto.PacketType.heartbeat:
        // Liveness only; no action needed beyond having received something.
        break;
      case proto.PacketType.telemetry:
        _telemetryController.add(proto.decodeTelemetry(data));
    }
  }

  /// Sends one AXIS packet. Called at the app's control-loop rate
  /// (30-50Hz), not per-frame.
  void sendAxis(proto.Axis axis) {
    _send(proto.encodeAxis(_nextSeq(), axis));
  }

  /// Sends a BUTTON event 3x in quick succession so a dropped UDP packet
  /// doesn't lose it.
  void sendButton(proto.Button button) {
    for (var i = 0; i < 3; i++) {
      _send(proto.encodeButton(_nextSeq(), button));
    }
  }

  int _nextSeq() {
    _seq = (_seq + 1) & 0xffff;
    return _seq;
  }

  void _send(Uint8List packet) {
    final addr = _serverAddress;
    final port = _serverPort;
    final socket = _socket;
    if (addr == null || port == null || socket == null) return;
    socket.send(packet, addr, port);
  }

  void disconnect() {
    _livenessTimer?.cancel();
    _socket?.close();
    _socket = null;
    _setStatus(ConnectionStatus.disconnected);
  }

  void dispose() {
    disconnect();
    _statusController.close();
    _telemetryController.close();
  }
}
