import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../network/protocol.dart' as proto;
import '../network/udp_client.dart';
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
    final connected = ref.watch(connectionStatusProvider).valueOrNull ==
        ConnectionStatus.connected;

    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            // Full-width rows are hard to scan on a landscape phone.
            constraints: const BoxConstraints(maxWidth: 720),
            child: ListView(
              padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
              children: [
                const _SectionHeader('Flying'),
                _Group(children: [
                  SwitchListTile(
                    secondary: const Icon(Icons.gamepad_outlined),
                    title: const Text('On-screen joystick'),
                    subtitle: const Text(
                        'Steer pitch and roll with a thumbstick instead of '
                        'tilting the phone'),
                    value: manualJoystick,
                    onChanged: (v) =>
                        ref.read(manualJoystickProvider.notifier).setEnabled(v),
                  ),
                  const Divider(height: 1, indent: 72),
                  SwitchListTile(
                    secondary: const Icon(Icons.exposure_outlined),
                    title: const Text('Throttle buttons'),
                    subtitle: const Text(
                        '+ and − buttons instead of the lever, with a hold '
                        'button for reverse'),
                    value: nudgeThrottle,
                    onChanged: (v) =>
                        ref.read(nudgeThrottleProvider.notifier).setEnabled(v),
                  ),
                ]),
                const _SectionHeader(
                  'Calibration',
                  caption: 'How the sim responds to each control. Saved on '
                      'the PC, so it applies to any phone you connect.',
                ),
                if (calibration == null)
                  _Group(children: [
                    ListTile(
                      leading: connected
                          ? const SizedBox.square(
                              dimension: 24,
                              child: CircularProgressIndicator(strokeWidth: 2))
                          : const Icon(Icons.link_off),
                      title: Text(connected
                          ? 'Loading from the server…'
                          : 'Not connected'),
                      subtitle: connected
                          ? null
                          : const Text(
                              'Connect to mhotas-server to edit calibration'),
                    ),
                  ])
                else ...[
                  _AxisCard(
                    label: 'Pitch',
                    icon: Icons.swap_vert,
                    axis: proto.ConfigAxis.pitch,
                    calibration: calibration.pitch,
                  ),
                  _AxisCard(
                    label: 'Roll',
                    icon: Icons.rotate_90_degrees_ccw,
                    axis: proto.ConfigAxis.roll,
                    calibration: calibration.roll,
                  ),
                  _AxisCard(
                    label: 'Throttle',
                    icon: Icons.speed,
                    axis: proto.ConfigAxis.throttle,
                    calibration: calibration.throttle,
                  ),
                  _AxisCard(
                    label: 'Rudder',
                    icon: Icons.swap_horiz,
                    axis: proto.ConfigAxis.rudder,
                    calibration: calibration.rudder,
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.title, {this.caption});

  final String title;
  final String? caption;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(4, 24, 4, 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title,
              style: theme.textTheme.titleSmall
                  ?.copyWith(color: theme.colorScheme.primary)),
          if (caption != null) ...[
            const SizedBox(height: 4),
            Text(caption!,
                style: theme.textTheme.bodySmall
                    ?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
          ],
        ],
      ),
    );
  }
}

class _Group extends StatelessWidget {
  const _Group({required this.children});

  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    return Card.filled(
      // Not the default surfaceContainerHighest: that's the slider's
      // inactive track color, which would vanish into it.
      color: Theme.of(context).colorScheme.surfaceContainerLow,
      margin: const EdgeInsets.only(bottom: 8),
      clipBehavior: Clip.antiAlias,
      child: Column(children: children),
    );
  }
}

/// Sends one CONFIG_SET per slider release, not per drag frame, to spare the
/// server's config file writes.
class _AxisCard extends ConsumerStatefulWidget {
  const _AxisCard({
    required this.label,
    required this.icon,
    required this.axis,
    required this.calibration,
  });

  final String label;
  final IconData icon;
  final int axis;
  final proto.AxisCalibration calibration;

  @override
  ConsumerState<_AxisCard> createState() => _AxisCardState();
}

class _AxisCardState extends ConsumerState<_AxisCard> {
  late double _deadZone = widget.calibration.deadZone;
  late double _expo = widget.calibration.expo;
  late double _smoothing = widget.calibration.smoothing;
  late bool _invert = widget.calibration.invert;
  late bool _signedThrottle = widget.calibration.signedThrottle;
  late double _signedThrottleIdle = widget.calibration.signedThrottleIdle;

  bool get _isThrottle => widget.axis == proto.ConfigAxis.throttle;

  @override
  void didUpdateWidget(_AxisCard old) {
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

  void _toggle(void Function() change) {
    setState(change);
    _commit();
  }

  String get _summary {
    String pct(double v) => '${(v * 100).round()}%';
    return [
      'Dead zone ${pct(_deadZone)}',
      'Curve ${pct(_expo)}',
      if (_smoothing > 0) 'Smoothing ${pct(_smoothing)}',
      if (_invert) 'Inverted',
      if (_isThrottle && _signedThrottle) 'Reverse on axis',
    ].join(' · ');
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card.filled(
      color: theme.colorScheme.surfaceContainerLow,
      margin: const EdgeInsets.only(bottom: 8),
      clipBehavior: Clip.antiAlias,
      child: ExpansionTile(
        key: PageStorageKey(widget.label),
        leading: Icon(widget.icon),
        title: Text(widget.label),
        subtitle: Text(_summary,
            style: theme.textTheme.bodySmall
                ?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
        shape: const Border(),
        collapsedShape: const Border(),
        childrenPadding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
        children: [
          _CurvePreview(deadZone: _deadZone, expo: _expo, invert: _invert),
          const SizedBox(height: 8),
          _LabeledSlider(
            label: 'Dead zone',
            help: 'Ignores small movements around center',
            value: _deadZone,
            max: 0.5,
            onChanged: (v) => setState(() => _deadZone = v),
            onChangeEnd: (_) => _commit(),
          ),
          _LabeledSlider(
            label: 'Response curve',
            help: 'Gentler near center, full at the ends',
            value: _expo,
            onChanged: (v) => setState(() => _expo = v),
            onChangeEnd: (_) => _commit(),
          ),
          _LabeledSlider(
            label: 'Smoothing',
            help: 'Filters out jitter, at the cost of a little lag',
            value: _smoothing,
            onChanged: (v) => setState(() => _smoothing = v),
            onChangeEnd: (_) => _commit(),
          ),
          SwitchListTile(
            contentPadding: EdgeInsets.zero,
            title: const Text('Invert'),
            subtitle: const Text('Turn on if the sim moves the wrong way'),
            value: _invert,
            onChanged: (v) => _toggle(() => _invert = v),
          ),
          if (_isThrottle) ...[
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('Reverse thrust on the axis'),
              subtitle: const Text(
                  'The bottom of the lever becomes reverse thrust. Only for '
                  'aircraft with reverse on the throttle axis; on others, '
                  'idle would sit above zero thrust.'),
              value: _signedThrottle,
              onChanged: (v) => _toggle(() => _signedThrottle = v),
            ),
            if (_signedThrottle)
              _LabeledSlider(
                label: 'Idle position',
                help: "Where idle sits on the axis; match the aircraft's "
                    'idle detent',
                value: _signedThrottleIdle,
                min: -1,
                format: (v) => v.toStringAsFixed(2),
                onChanged: (v) => setState(() => _signedThrottleIdle = v),
                onChangeEnd: (_) => _commit(),
              ),
          ],
        ],
      ),
    );
  }
}

class _LabeledSlider extends StatelessWidget {
  const _LabeledSlider({
    required this.label,
    required this.help,
    required this.value,
    required this.onChanged,
    required this.onChangeEnd,
    this.min = 0,
    this.max = 1,
    this.format,
  });

  final String label;
  final String help;
  final double value;
  final double min;
  final double max;
  final String Function(double)? format;
  final ValueChanged<double> onChanged;
  final ValueChanged<double> onChangeEnd;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final display = format?.call(value) ?? '${(value * 100).round()}%';
    // One row per setting: stacked, a landscape phone fits two at a time.
    return Padding(
      padding: const EdgeInsets.only(top: 4),
      child: Row(
        children: [
          Expanded(
            flex: 2,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(label, style: theme.textTheme.bodyLarge),
                Text(help,
                    style: theme.textTheme.bodySmall
                        ?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
              ],
            ),
          ),
          Expanded(
            flex: 3,
            child: Slider(
              // A config file edited by hand can hold a value past this range.
              value: value.clamp(min, max),
              min: min,
              max: max,
              onChanged: onChanged,
              onChangeEnd: onChangeEnd,
            ),
          ),
          Container(
            width: 52,
            padding: const EdgeInsets.symmetric(vertical: 2),
            decoration: BoxDecoration(
              color: theme.colorScheme.secondaryContainer,
              borderRadius: BorderRadius.circular(8),
            ),
            child: Text(display,
                textAlign: TextAlign.center,
                style: theme.textTheme.labelMedium
                    ?.copyWith(color: theme.colorScheme.onSecondaryContainer)),
          ),
        ],
      ),
    );
  }
}

/// Input (x) to output (y), as the server's calibration.Curve.Apply shapes it.
double _applyCurve(double v, double deadZone, double expo, bool invert) {
  if (invert) v = -v;
  final sign = v < 0 ? -1.0 : 1.0;
  var a = v.abs();
  if (deadZone >= 1 || a <= deadZone) return 0;
  a = (a - deadZone) / (1 - deadZone);
  if (expo > 0) a = (1 - expo) * a + expo * a * a * a;
  return sign * a;
}

class _CurvePreview extends StatelessWidget {
  const _CurvePreview(
      {required this.deadZone, required this.expo, required this.invert});

  final double deadZone;
  final double expo;
  final bool invert;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      height: 100,
      decoration: BoxDecoration(
        color: scheme.surfaceContainerLowest,
        borderRadius: BorderRadius.circular(12),
      ),
      child: CustomPaint(
        painter: _CurvePainter(
          curve: (v) => _applyCurve(v, deadZone, expo, invert),
          line: scheme.primary,
          grid: scheme.outlineVariant,
        ),
        child: const SizedBox.expand(),
      ),
    );
  }
}

class _CurvePainter extends CustomPainter {
  _CurvePainter({required this.curve, required this.line, required this.grid});

  final double Function(double) curve;
  final Color line;
  final Color grid;

  @override
  void paint(Canvas canvas, Size size) {
    const pad = 12.0;
    final w = size.width - 2 * pad, h = size.height - 2 * pad;
    Offset at(double x, double y) =>
        Offset(pad + (x + 1) / 2 * w, pad + (1 - (y + 1) / 2) * h);

    final gridPaint = Paint()
      ..color = grid
      ..strokeWidth = 1;
    canvas.drawLine(at(-1, 0), at(1, 0), gridPaint);
    canvas.drawLine(at(0, -1), at(0, 1), gridPaint);
    canvas.drawLine(
        at(-1, -1), at(1, 1), gridPaint..color = grid.withAlpha(90));

    final path = Path();
    for (var i = 0; i <= 100; i++) {
      final x = -1 + 2 * i / 100;
      final p = at(x, curve(x));
      i == 0 ? path.moveTo(p.dx, p.dy) : path.lineTo(p.dx, p.dy);
    }
    canvas.drawPath(
        path,
        Paint()
          ..color = line
          ..strokeWidth = 2.5
          ..style = PaintingStyle.stroke);
  }

  @override
  bool shouldRepaint(_CurvePainter old) => true;
}
