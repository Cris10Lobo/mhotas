import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../state/connection.dart';
import '../network/udp_client.dart';
import 'control_screen.dart';
import 'scan_screen.dart';

const _prefsHostKey = 'mhotas.server.host';
const _prefsPortKey = 'mhotas.server.port';
const _defaultPort = 9750;

/// Manual IP:port entry, remembered locally — the whole of v1's connection
/// UX; auto-discovery is a later addition.
class ConnectScreen extends ConsumerStatefulWidget {
  const ConnectScreen({super.key});

  @override
  ConsumerState<ConnectScreen> createState() => _ConnectScreenState();
}

class _ConnectScreenState extends ConsumerState<ConnectScreen> {
  final _hostController = TextEditingController();
  final _portController = TextEditingController(text: '$_defaultPort');
  bool _connecting = false;
  String? _error;

  // HELLO over UDP has no delivery guarantee, so this can sit open
  // indefinitely if the entered address never responds — track it so
  // dispose() can close it if the user navigates away while still
  // "connecting", instead of leaking a listener closing over this widget.
  ProviderSubscription<AsyncValue<ConnectionStatus>>? _statusSub;

  @override
  void initState() {
    super.initState();
    _loadSavedAddress();
  }

  Future<void> _loadSavedAddress() async {
    final prefs = await SharedPreferences.getInstance();
    final host = prefs.getString(_prefsHostKey);
    final port = prefs.getInt(_prefsPortKey);
    if (!mounted) return;
    if (host != null) _hostController.text = host;
    if (port != null) _portController.text = '$port';
  }

  Future<void> _connect() async {
    final host = _hostController.text.trim();
    final port = int.tryParse(_portController.text.trim());
    if (host.isEmpty || port == null) {
      setState(() => _error = 'Enter a valid host and port');
      return;
    }

    setState(() {
      _connecting = true;
      _error = null;
    });

    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_prefsHostKey, host);
    await prefs.setInt(_prefsPortKey, port);

    final client = ref.read(udpClientProvider);
    _statusSub?.close(); // in case a previous attempt never resolved
    _statusSub = ref.listenManual(connectionStatusProvider, (previous, next) {
      final status = next.valueOrNull;
      if (status == null) return;
      if (status == ConnectionStatus.connected) {
        _closeStatusSub();
        if (!mounted) return;
        Navigator.of(context).pushReplacement(
          MaterialPageRoute(builder: (_) => const ControlScreen()),
        );
      } else if (status == ConnectionStatus.versionMismatch) {
        _closeStatusSub();
        if (!mounted) return;
        setState(() {
          _connecting = false;
          _error =
              'Server protocol version mismatch — update the app or server';
        });
      } else if (status == ConnectionStatus.error) {
        _closeStatusSub();
        if (!mounted) return;
        setState(() {
          _connecting = false;
          _error = 'Could not resolve that address';
        });
      }
    });

    await client.connect(host, port);
  }

  void _closeStatusSub() {
    _statusSub?.close();
    _statusSub = null;
  }

  Future<void> _scanAndConnect() async {
    final result = await Navigator.of(context).push<(String, int)>(
      MaterialPageRoute(builder: (_) => const ScanScreen()),
    );
    if (result == null || !mounted) return;
    final (host, port) = result;
    _hostController.text = host;
    _portController.text = '$port';
    await _connect();
  }

  @override
  void dispose() {
    _closeStatusSub();
    _hostController.dispose();
    _portController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('mHOTAS')),
      // SingleChildScrollView, not a plain Padding+Column: the "Scan QR
      // code" button + divider added above the original two fields pushed
      // total content height past what fits on a shorter screen (or with
      // the keyboard up), overflowing a fixed Column — reported live.
      body: SingleChildScrollView(
        padding: const EdgeInsets.all(24),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const Text(
              'Connect to mhotas-server',
              style: TextStyle(fontSize: 20, fontWeight: FontWeight.bold),
            ),
            const SizedBox(height: 24),
            OutlinedButton.icon(
              onPressed: _connecting ? null : _scanAndConnect,
              icon: const Icon(Icons.qr_code_scanner),
              label: const Text('Scan QR code'),
            ),
            const SizedBox(height: 16),
            const Row(
              children: [
                Expanded(child: Divider()),
                Padding(
                  padding: EdgeInsets.symmetric(horizontal: 12),
                  child: Text('or enter manually'),
                ),
                Expanded(child: Divider()),
              ],
            ),
            const SizedBox(height: 16),
            TextField(
              controller: _hostController,
              decoration: const InputDecoration(labelText: 'Server IP address'),
              keyboardType: TextInputType.number,
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _portController,
              decoration: const InputDecoration(labelText: 'Port'),
              keyboardType: TextInputType.number,
            ),
            const SizedBox(height: 24),
            if (_error != null)
              Padding(
                padding: const EdgeInsets.only(bottom: 12),
                child: Text(_error!, style: const TextStyle(color: Colors.red)),
              ),
            FilledButton(
              onPressed: _connecting ? null : _connect,
              child: Text(_connecting ? 'Connecting...' : 'Connect'),
            ),
          ],
        ),
      ),
    );
  }
}
