import 'package:flutter/material.dart';

import '../state/status_tiles.dart';

/// MVP-minimal by design: curve/dead-zone editing stays server-config-file-
/// only (server/internal/config) until a later remote-settings-sync
/// feature. The one thing that is configurable here is which simvar each
/// control-screen status tile shows.
class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  List<StatusTileKind> _kinds = defaultStatusTileKinds;

  @override
  void initState() {
    super.initState();
    _loadKinds();
  }

  Future<void> _loadKinds() async {
    final kinds = await loadStatusTileKinds();
    if (!mounted) return;
    setState(() => _kinds = kinds);
  }

  Future<void> _setKind(int tileIndex, StatusTileKind kind) async {
    setState(() => _kinds = [
          for (var i = 0; i < _kinds.length; i++)
            i == tileIndex ? kind : _kinds[i],
        ]);
    await saveStatusTileKind(tileIndex, kind);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        padding: const EdgeInsets.all(24),
        children: [
          const Text(
            'Status tiles',
            style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 8),
          const Text(
            'Choose what each of the 4 status tiles on the flying screen '
            'shows. Duplicates are fine.',
          ),
          const SizedBox(height: 16),
          for (var i = 0; i < statusTileCount; i++)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Row(
                children: [
                  Text('Tile ${i + 1}'),
                  const SizedBox(width: 16),
                  DropdownButton<StatusTileKind>(
                    value: _kinds[i],
                    items: [
                      for (final kind in StatusTileKind.values)
                        DropdownMenuItem(value: kind, child: Text(kind.label)),
                    ],
                    onChanged: (kind) {
                      if (kind != null) _setKind(i, kind);
                    },
                  ),
                ],
              ),
            ),
          const SizedBox(height: 24),
          const Text(
            'Calibration (dead-zone, curve, invert) lives in the server\'s '
            'config file for now — remote settings editing from the phone '
            'is planned for later.',
          ),
        ],
      ),
    );
  }
}
