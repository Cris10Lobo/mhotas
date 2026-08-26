import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../network/protocol.dart' as proto;
import '../state/calibration.dart';
import '../state/connection.dart';
import '../state/settings.dart';
import '../state/status_tiles.dart';

/// Two things are configurable purely on-device: which simvar each
/// control-screen status tile shows, and the manual joystick toggle. Axis
/// calibration (dead-zone/expo/invert/smoothing) lives on the server but is
/// editable from here over CONFIG/CONFIG_SET (see docs/protocol.md) —
/// requesting a fresh read on open rather than trusting whatever the
/// connect-time push happened to deliver, since this screen may not have
/// been mounted yet when that one-shot CONFIG arrived.
class SettingsScreen extends ConsumerStatefulWidget {
  const SettingsScreen({super.key});

  @override
  ConsumerState<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends ConsumerState<SettingsScreen> {
  List<StatusTileKind> _kinds = defaultStatusTileKinds;

  @override
  void initState() {
    super.initState();
    _loadKinds();
    ref.read(udpClientProvider).requestConfig();
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
    final manualJoystick = ref.watch(manualJoystickProvider);
    final calibration = ref.watch(calibrationProvider).valueOrNull;
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
          SwitchListTile(
            title: const Text('Manual joystick'),
            subtitle: const Text(
                'Use an on-screen joystick for pitch/roll instead of tilting the phone'),
            value: manualJoystick,
            onChanged: (v) =>
                ref.read(manualJoystickProvider.notifier).setEnabled(v),
          ),
          const SizedBox(height: 24),
          const Text(
            'Calibration',
            style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 8),
          const Text(
            'Dead-zone, response curve, invert, and smoothing — applied on '
            'the server, saved to its config file. Failsafe policy isn\'t '
            'editable here; it stays a server-config-only setting.',
          ),
          const SizedBox(height: 16),
          if (calibration == null)
            const Padding(
              padding: EdgeInsets.symmetric(vertical: 24),
              child: Center(child: CircularProgressIndicator()),
            )
          else ...[
            _AxisCalibrationCard(
              label: 'Pitch',
              axis: proto.ConfigAxis.pitch,
              calibration: calibration.pitch,
            ),
            _AxisCalibrationCard(
              label: 'Roll',
              axis: proto.ConfigAxis.roll,
              calibration: calibration.roll,
            ),
            _AxisCalibrationCard(
              label: 'Throttle',
              axis: proto.ConfigAxis.throttle,
              calibration: calibration.throttle,
            ),
            _AxisCalibrationCard(
              label: 'Rudder',
              axis: proto.ConfigAxis.rudder,
              calibration: calibration.rudder,
            ),
          ],
        ],
      ),
    );
  }
}

/// One axis's dead-zone/expo/invert/smoothing, editable and sent to the
/// server as a single CONFIG_SET on each slider release (not per-frame
/// while dragging — these are rare edits, not a continuous control like the
/// throttle lever, so there's no reason to flood the link or the config
/// file's disk writes). [calibration] is the server's last-known value for
/// this axis; local drag state only ever diverges from it mid-gesture.
class _AxisCalibrationCard extends ConsumerStatefulWidget {
  const _AxisCalibrationCard({
    required this.label,
    required this.axis,
    required this.calibration,
  });

  final String label;
  final int axis; // proto.ConfigAxis.*
  final proto.AxisCalibration calibration;

  @override
  ConsumerState<_AxisCalibrationCard> createState() =>
      _AxisCalibrationCardState();
}

class _AxisCalibrationCardState extends ConsumerState<_AxisCalibrationCard> {
  late double _deadZone = widget.calibration.deadZone;
  late double _expo = widget.calibration.expo;
  late double _smoothing = widget.calibration.smoothing;
  late bool _invert = widget.calibration.invert;

  @override
  void didUpdateWidget(_AxisCalibrationCard old) {
    super.didUpdateWidget(old);
    // The server only ever sends CONFIG in reply to our own request or our
    // own edit, so there's no "someone else is editing it live" race to
    // guard against the way the throttle lever's telemetry sync needs to —
    // always take the freshest known value.
    _deadZone = widget.calibration.deadZone;
    _expo = widget.calibration.expo;
    _smoothing = widget.calibration.smoothing;
    _invert = widget.calibration.invert;
  }

  void _commit() {
    ref.read(udpClientProvider).sendConfigSet(proto.ConfigSet(
          axis: widget.axis,
          calibration: proto.AxisCalibration(
            deadZone: _deadZone,
            expo: _expo,
            invert: _invert,
            smoothing: _smoothing,
          ),
        ));
  }

  @override
  Widget build(BuildContext context) {
    return Card(
      margin: const EdgeInsets.symmetric(vertical: 8),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(widget.label,
                style:
                    const TextStyle(fontSize: 16, fontWeight: FontWeight.bold)),
            _CalibrationSlider(
              label: 'Dead-zone',
              value: _deadZone,
              onChanged: (v) => setState(() => _deadZone = v),
              onChangeEnd: (_) => _commit(),
            ),
            _CalibrationSlider(
              label: 'Expo',
              value: _expo,
              onChanged: (v) => setState(() => _expo = v),
              onChangeEnd: (_) => _commit(),
            ),
            _CalibrationSlider(
              label: 'Smoothing',
              value: _smoothing,
              onChanged: (v) => setState(() => _smoothing = v),
              onChangeEnd: (_) => _commit(),
            ),
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('Invert'),
              value: _invert,
              onChanged: (v) {
                setState(() => _invert = v);
                _commit();
              },
            ),
          ],
        ),
      ),
    );
  }
}

class _CalibrationSlider extends StatelessWidget {
  const _CalibrationSlider({
    required this.label,
    required this.value,
    required this.onChanged,
    required this.onChangeEnd,
  });

  final String label;
  final double value;
  final ValueChanged<double> onChanged;
  final ValueChanged<double> onChangeEnd;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        SizedBox(width: 90, child: Text(label)),
        Expanded(
          child: Slider(
            value: value.clamp(0, 1),
            onChanged: onChanged,
            onChangeEnd: onChangeEnd,
          ),
        ),
        SizedBox(
          width: 40,
          child: Text('${(value * 100).round()}%', textAlign: TextAlign.end),
        ),
      ],
    );
  }
}
