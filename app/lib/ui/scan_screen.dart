import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

/// Full-screen QR scanner for the server's TUI dashboard QR code
/// (server/internal/tui). The server encodes plain "ip:port" — the exact
/// string connect_screen.dart's manual host/port fields already expect —
/// so scanning needs no protocol change on either side, just a shortcut
/// past typing it in by hand.
class ScanScreen extends StatefulWidget {
  const ScanScreen({super.key});

  @override
  State<ScanScreen> createState() => _ScanScreenState();
}

class _ScanScreenState extends State<ScanScreen> {
  final _controller = MobileScannerController(formats: [BarcodeFormat.qrCode]);

  // onDetect can fire multiple times for the same code while the camera
  // keeps streaming frames before the pop actually unmounts this screen —
  // without this guard, a slow-to-navigate-away frame burst could try to
  // pop twice.
  bool _handled = false;

  void _onDetect(BarcodeCapture capture) {
    if (_handled || capture.barcodes.isEmpty) return;
    final raw = capture.barcodes.first.rawValue;
    if (raw == null) return;
    final parsed = _parseHostPort(raw);
    if (parsed == null) {
      return; // not a mhotas QR (or a garbled read) — keep scanning
    }
    _handled = true;
    Navigator.of(context).pop(parsed);
  }

  /// Parses "host:port" — splits on the *last* colon so a bare IPv4 host
  /// (the only kind the server currently binds/encodes, see
  /// docs/protocol.md) works without ambiguity.
  static (String, int)? _parseHostPort(String raw) {
    final i = raw.lastIndexOf(':');
    if (i <= 0 || i == raw.length - 1) return null;
    final port = int.tryParse(raw.substring(i + 1));
    if (port == null) return null;
    return (raw.substring(0, i), port);
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Scan server QR code')),
      body: Stack(
        fit: StackFit.expand,
        children: [
          MobileScanner(controller: _controller, onDetect: _onDetect),
          IgnorePointer(
            child: Center(
              child: Container(
                width: 240,
                height: 240,
                decoration: BoxDecoration(
                  border: Border.all(color: Colors.white70, width: 3),
                  borderRadius: BorderRadius.circular(16),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
