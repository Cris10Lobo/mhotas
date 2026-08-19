import 'package:flutter/material.dart';

/// MVP-minimal by design: curve/dead-zone editing stays server-config-file-
/// only (server/internal/config) until a later remote-settings-sync
/// feature; this screen exists as a placeholder for that.
class SettingsScreen extends StatelessWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: const Center(
        child: Padding(
          padding: EdgeInsets.all(24),
          child: Text(
            'Nothing to configure here yet.\n\n'
            'Calibration (dead-zone, curve, invert) lives in the server\'s '
            'config file for now — remote settings editing from the phone '
            'is planned for later.',
            textAlign: TextAlign.center,
          ),
        ),
      ),
    );
  }
}
