import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../network/udp_client.dart';
import '../state/connection.dart';
import 'brand.dart';
import 'control_screen.dart';
import 'scan_screen.dart';

const _prefsHostKey = 'mhotas.server.host';
const _prefsPortKey = 'mhotas.server.port';
const _defaultPort = 9750;

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

  // Closed in dispose() so an unanswered HELLO doesn't leak the listener.
  ProviderSubscription<AsyncValue<ConnectionStatus>>? _statusSub;

  // UDP gives no failure signal; without this an unreachable address leaves the button stuck on "Connecting...".
  static const _connectTimeout = Duration(seconds: 6);
  Timer? _timeoutTimer;

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
    _statusSub?.close();
    _statusSub = ref.listenManual(connectionStatusProvider, (previous, next) {
      final status = next.valueOrNull;
      if (status == null) return;
      if (status == ConnectionStatus.connected) {
        _stopWaiting();
        if (!mounted) return;
        Navigator.of(context).pushReplacement(
          MaterialPageRoute(builder: (_) => const ControlScreen()),
        );
      } else if (status == ConnectionStatus.versionMismatch) {
        _stopWaiting();
        if (!mounted) return;
        setState(() {
          _connecting = false;
          _error = 'Server protocol version mismatch: update the app or server';
        });
      } else if (status == ConnectionStatus.error) {
        _stopWaiting();
        if (!mounted) return;
        setState(() {
          _connecting = false;
          _error = 'Could not resolve that address';
        });
      }
    });

    _timeoutTimer?.cancel();
    _timeoutTimer = Timer(_connectTimeout, () {
      _stopWaiting();
      if (!mounted) return;
      setState(() {
        _connecting = false;
        _error =
            'Timed out waiting for the server. Check it\'s running and the '
            'address is reachable from this phone';
      });
    });

    await client.connect(host, port);
  }

  void _stopWaiting() {
    _statusSub?.close();
    _statusSub = null;
    _timeoutTimer?.cancel();
    _timeoutTimer = null;
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
    _stopWaiting();
    _hostController.dispose();
    _portController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;

    final scan = Material(
      color: scheme.secondaryContainer,
      borderRadius: BorderRadius.circular(20),
      child: InkWell(
        borderRadius: BorderRadius.circular(20),
        onTap: _connecting ? null : _scanAndConnect,
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(Icons.qr_code_scanner,
                  size: 48, color: scheme.onSecondaryContainer),
              const SizedBox(height: 12),
              Text('Scan QR code',
                  style: theme.textTheme.titleMedium?.copyWith(
                      color: scheme.onSecondaryContainer,
                      fontWeight: FontWeight.w600)),
              const SizedBox(height: 4),
              Text(
                'Point the camera at the code in the server window',
                textAlign: TextAlign.center,
                style: theme.textTheme.bodySmall
                    ?.copyWith(color: scheme.onSecondaryContainer),
              ),
            ],
          ),
        ),
      ),
    );

    final manual = Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('Enter address',
            style: theme.textTheme.titleMedium
                ?.copyWith(fontWeight: FontWeight.w600)),
        const SizedBox(height: 12),
        Row(
          children: [
            Expanded(
              flex: 3,
              child: TextField(
                controller: _hostController,
                keyboardType: TextInputType.number,
                textInputAction: TextInputAction.next,
                decoration: const InputDecoration(
                  labelText: 'Server IP',
                  hintText: '192.168.1.20',
                  prefixIcon: Icon(Icons.computer),
                  border: OutlineInputBorder(),
                  isDense: true,
                ),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: TextField(
                controller: _portController,
                keyboardType: TextInputType.number,
                textInputAction: TextInputAction.go,
                onSubmitted: (_) => _connecting ? null : _connect(),
                decoration: const InputDecoration(
                  labelText: 'Port',
                  border: OutlineInputBorder(),
                  isDense: true,
                ),
              ),
            ),
          ],
        ),
        if (_error != null) ...[
          const SizedBox(height: 10),
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Icon(Icons.error_outline, size: 18, color: scheme.error),
              const SizedBox(width: 6),
              Expanded(
                child: Text(_error!,
                    style: theme.textTheme.bodySmall
                        ?.copyWith(color: scheme.error)),
              ),
            ],
          ),
        ],
        const SizedBox(height: 12),
        FilledButton.icon(
          onPressed: _connecting ? null : _connect,
          style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(48)),
          icon: _connecting
              ? const SizedBox.square(
                  dimension: 18,
                  child: CircularProgressIndicator(strokeWidth: 2))
              : const Icon(Icons.link),
          label: Text(_connecting ? 'Connecting...' : 'Connect'),
        ),
      ],
    );

    return Scaffold(
      appBar: AppBar(title: const BrandTitle()),
      body: SafeArea(
        child: LayoutBuilder(
          builder: (context, constraints) {
            final wide = constraints.maxWidth >= 600;
            final content = wide
                ? IntrinsicHeight(
                    child: Row(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        Expanded(child: scan),
                        const SizedBox(width: 24),
                        Expanded(child: Center(child: manual)),
                      ],
                    ),
                  )
                : Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [scan, const SizedBox(height: 24), manual],
                  );
            // Scrollable so the keyboard can't cause an overflow; otherwise it fits on one screen.
            return SingleChildScrollView(
              padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 16),
              child: ConstrainedBox(
                constraints:
                    BoxConstraints(minHeight: constraints.maxHeight - 32),
                child: Center(
                  child: ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 760),
                    child: content,
                  ),
                ),
              ),
            );
          },
        ),
      ),
    );
  }
}
