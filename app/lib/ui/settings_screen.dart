import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/settings.dart';

/// MVP-minimal by design: curve/dead-zone editing stays server-config-file-
/// only (server/internal/config) until a later remote-settings-sync
/// feature. The manual joystick toggle lives here instead, since it's a
/// choice about which on-screen control drives pitch/roll, not something
/// the server needs to know about.
class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final manualJoystick = ref.watch(manualJoystickProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          SwitchListTile(
            title: const Text('Manual joystick'),
            subtitle: const Text(
                'Use an on-screen joystick for pitch/roll instead of tilting the phone'),
            value: manualJoystick,
            onChanged: (v) =>
                ref.read(manualJoystickProvider.notifier).setEnabled(v),
          ),
          const Padding(
            padding: EdgeInsets.only(top: 24),
            child: Text(
              'Calibration (dead-zone, curve, invert) lives in the server\'s '
              'config file for now — remote settings editing from the phone '
              'is planned for later.',
              textAlign: TextAlign.center,
            ),
          ),
        ],
      ),
    );
  }
}
