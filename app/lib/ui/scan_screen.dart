import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

/// Scans the server's TUI QR code, which encodes plain "ip:port".
class ScanScreen extends StatefulWidget {
  const ScanScreen({super.key});

  @override
  State<ScanScreen> createState() => _ScanScreenState();
}

class _ScanScreenState extends State<ScanScreen> {
  final _controller = MobileScannerController(formats: [BarcodeFormat.qrCode]);

  // onDetect keeps firing until the pop unmounts the screen; avoid popping twice.
  bool _handled = false;

  void _onDetect(BarcodeCapture capture) {
    if (_handled || capture.barcodes.isEmpty) return;
    final raw = capture.barcodes.first.rawValue;
    if (raw == null) return;
    final parsed = _parseHostPort(raw);
    if (parsed == null) return;
    _handled = true;
    Navigator.of(context).pop(parsed);
  }

  /// Splits on the last colon; the server only encodes bare IPv4 hosts.
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
