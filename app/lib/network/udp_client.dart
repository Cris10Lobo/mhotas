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

class UdpClient {
  UdpClient({required this.deviceName});

  final String deviceName;

  RawDatagramSocket? _socket;
  InternetAddress? _serverAddress;
  int? _serverPort;
  int _seq = 0;

  final _statusController = StreamController<ConnectionStatus>.broadcast();
  final _telemetryController = StreamController<proto.Telemetry>.broadcast();
  final _configController = StreamController<proto.Config>.broadcast();
  ConnectionStatus _lastStatus = ConnectionStatus.disconnected;

  // The server pings at ~1Hz; while silent past the timeout, the 1s tick also
  // resends HELLO so the app reconnects by itself when the link returns.
  Timer? _livenessTimer;
  DateTime _lastRxAt = DateTime.fromMillisecondsSinceEpoch(0);
  static const _livenessTimeout = Duration(seconds: 3);

  Stream<ConnectionStatus> get status => _statusController.stream;
  Stream<proto.Telemetry> get telemetry => _telemetryController.stream;
  Stream<proto.Config> get config => _configController.stream;

  void _setStatus(ConnectionStatus s) {
    _lastStatus = s;
    _statusController.add(s);
  }

  /// Sends HELLO; connected is only reported once HELLO_ACK arrives on [status].
  Future<void> connect(String host, int port) async {
    _setStatus(ConnectionStatus.connecting);
    try {
      _serverAddress = InternetAddress(host);
    } on FormatException {
      _setStatus(ConnectionStatus.error);
      return;
    }
    _serverPort = port;

    // Listen once: the socket stream is single-subscription, and retries after
    // a version-mismatch or resolve error don't go through disconnect().
    if (_socket == null) {
      _socket = await RawDatagramSocket.bind(InternetAddress.anyIPv4, 0);
      _socket!.listen(_onSocketEvent);
    }

    _lastRxAt = DateTime.now();
    _livenessTimer?.cancel();
    _livenessTimer =
        Timer.periodic(const Duration(seconds: 1), (_) => _checkLiveness());

    _sendHello();
  }

  void _sendHello() =>
      _send(proto.encodeHello(_nextSeq(), proto.Hello(deviceName: deviceName)));

  void _checkLiveness() {
    if (DateTime.now().difference(_lastRxAt) <= _livenessTimeout) return;
    if (_lastStatus == ConnectionStatus.connected) {
      _setStatus(ConnectionStatus.disconnected);
    }
    _sendHello();
  }

  void _onSocketEvent(RawSocketEvent event) {
    if (event != RawSocketEvent.read) return;
    final datagram = _socket?.receive();
    if (datagram == null) return;
    _lastRxAt = DateTime.now();
    _handlePacket(datagram.data);
  }

  void _handlePacket(Uint8List data) {
    // Decoders throw on truncated packets; keep that out of the socket listener.
    try {
      _handleDecodedPacket(proto.Header.decode(data), data);
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
        break;
      case proto.PacketType.telemetry:
        _telemetryController.add(proto.decodeTelemetry(data));
      case proto.PacketType.config:
        _configController.add(proto.decodeConfig(data));
    }
  }

  void sendAxis(proto.Axis axis) {
    _send(proto.encodeAxis(_nextSeq(), axis));
  }

  /// Sent 3x so a dropped packet doesn't lose the event; the server dedupes.
  void sendButton(proto.Button button) {
    for (var i = 0; i < 3; i++) {
      _send(proto.encodeButton(_nextSeq(), button));
    }
  }

  /// The server replies with a fresh CONFIG on [config].
  void sendConfigSet(proto.ConfigSet cs) {
    _send(proto.encodeConfigSet(_nextSeq(), cs));
  }

  void requestConfig() {
    _send(proto.encodeConfigGet(_nextSeq()));
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
    _configController.close();
  }
}
