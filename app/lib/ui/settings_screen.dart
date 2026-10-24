import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../network/protocol.dart' as proto;
import '../state/calibration.dart';
import '../state/connection.dart';
import '../state/settings.dart';

class SettingsScreen extends ConsumerStatefulWidget {
  const SettingsScreen({super.key});

  @override
  ConsumerState<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends ConsumerState<SettingsScreen> {
  @override
  void initState() {
    super.initState();
    ref.read(udpClientProvider).requestConfig();
  }

  @override
  Widget build(BuildContext context) {
    final manualJoystick = ref.watch(manualJoystickProvider);
    final nudgeThrottle = ref.watch(nudgeThrottleProvider);
    final calibration = ref.watch(calibrationProvider).valueOrNull;
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        padding: const EdgeInsets.all(24),
        children: [
          SwitchListTile(
            title: const Text('Manual joystick'),
            subtitle: const Text(
                'Use an on-screen joystick for pitch/roll instead of tilting the phone'),
            value: manualJoystick,
            onChanged: (v) =>
                ref.read(manualJoystickProvider.notifier).setEnabled(v),
          ),
          SwitchListTile(
            title: const Text('Nudge throttle'),
            subtitle: const Text(
                'Use +/- buttons instead of the throttle lever, with a hold button for reverse'),
            value: nudgeThrottle,
            onChanged: (v) =>
                ref.read(nudgeThrottleProvider.notifier).setEnabled(v),
          ),
          const SizedBox(height: 24),
          const Text(
            'Calibration',
            style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 8),
          const Text(
            'Dead-zone, response curve, invert, and smoothing, applied on '
            'the server and saved to its config file. Failsafe policy is '
            'only editable in the server config.',
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

/// Sends one CONFIG_SET per slider release, not per drag frame, to spare the server's config file writes.
class _AxisCalibrationCard extends ConsumerStatefulWidget {
  const _AxisCalibrationCard({
    required this.label,
    required this.axis,
    required this.calibration,
  });

  final String label;
  final int axis;
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
  late bool _signedThrottle = widget.calibration.signedThrottle;
  late double _signedThrottleIdle = widget.calibration.signedThrottleIdle;

  @override
  void didUpdateWidget(_AxisCalibrationCard old) {
    super.didUpdateWidget(old);
    _deadZone = widget.calibration.deadZone;
    _expo = widget.calibration.expo;
    _smoothing = widget.calibration.smoothing;
    _invert = widget.calibration.invert;
    _signedThrottle = widget.calibration.signedThrottle;
    _signedThrottleIdle = widget.calibration.signedThrottleIdle;
  }

  void _commit() {
    ref.read(udpClientProvider).sendConfigSet(proto.ConfigSet(
          axis: widget.axis,
          calibration: proto.AxisCalibration(
            deadZone: _deadZone,
            expo: _expo,
            invert: _invert,
            smoothing: _smoothing,
            signedThrottle: _signedThrottle,
            signedThrottleIdle: _signedThrottleIdle,
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
            if (widget.axis == proto.ConfigAxis.throttle) ...[
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                title: const Text('Signed reverse thrust'),
                subtitle: const Text(
                    'Send lever position directly, with no separate arm '
                    'step. Some aircraft need this instead of the default.'),
                value: _signedThrottle,
                onChanged: (v) {
                  setState(() => _signedThrottle = v);
                  _commit();
                },
              ),
              if (_signedThrottle)
                _CalibrationSlider(
                  label: 'Idle point',
                  value: _signedThrottleIdle,
                  min: -1,
                  max: 1,
                  onChanged: (v) => setState(() => _signedThrottleIdle = v),
                  onChangeEnd: (_) => _commit(),
                ),
            ],
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
    this.min = 0,
    this.max = 1,
  });

  final String label;
  final double value;
  final double min;
  final double max;
  final ValueChanged<double> onChanged;
  final ValueChanged<double> onChangeEnd;

  @override
  Widget build(BuildContext context) {
    final display = min == 0 && max == 1
        ? '${(value * 100).round()}%'
        : value.toStringAsFixed(2);
    return Row(
      children: [
        SizedBox(width: 90, child: Text(label)),
        Expanded(
          child: Slider(
            value: value.clamp(min, max),
            min: min,
            max: max,
            onChanged: onChanged,
            onChangeEnd: onChangeEnd,
          ),
        ),
        SizedBox(width: 48, child: Text(display, textAlign: TextAlign.end)),
      ],
    );
  }
}
