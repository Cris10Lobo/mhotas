import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:wakelock_plus/wakelock_plus.dart';

import '../network/protocol.dart' as proto;
import '../network/udp_client.dart';
import '../sensors/attitude.dart';
import '../state/connection.dart';
import '../state/settings.dart';
import '../state/slider_axis.dart';
import '../state/status_tiles.dart';
import '../state/telemetry.dart';
import 'settings_screen.dart';

/// Button IDs used by this screen's MVP button set — must match whatever
/// the server config maps them to (server/internal/config).
class ButtonIds {
  static const int gear = 1;
  static const int parkingBrake = 2;
  static const int pause = 3;
  static const int flapIncr = 4;
  static const int flapDecr = 5;
}

/// The primary flying screen, styled after X-Plane Mobile: tilt the phone
/// for pitch/roll, use the on-screen sliders for throttle/rudder, glance
/// at large buttons and the flap/gear telemetry readout — controls are
/// sized for occasional glances, not sustained attention.
class ControlScreen extends ConsumerStatefulWidget {
  const ControlScreen({super.key});

  @override
  ConsumerState<ControlScreen> createState() => _ControlScreenState();
}

class _ControlScreenState extends ConsumerState<ControlScreen> {
  final _attitudeStream = AttitudeStream();
  Attitude _attitude = const Attitude(pitch: 0, roll: 0);
  // x: roll, y: pitch — same axis mapping _TiltIndicator already uses for
  // Alignment(roll, pitch), so the joystick reads the same way the tilt
  // display does.
  Offset _joystick = Offset.zero;
  StreamSubscription<Attitude>? _attitudeSub;
  Timer? _sendTimer;
  int _buttonEventId = 0;
  List<StatusTileKind> _tileKinds = defaultStatusTileKinds;

  @override
  void initState() {
    super.initState();
    WakelockPlus.enable(); // screen must not sleep mid-flight
    _attitudeStream.start();
    _attitudeSub = _attitudeStream.stream.listen((a) {
      if (mounted) setState(() => _attitude = a);
    });
    _sendTimer = Timer.periodic(
        const Duration(milliseconds: 33), (_) => _sendAxis()); // ~30Hz
    _loadTileKinds();
  }

  Future<void> _loadTileKinds() async {
    final kinds = await loadStatusTileKinds();
    if (!mounted) return;
    setState(() => _tileKinds = kinds);
  }

  Future<void> _openSettings() async {
    await Navigator.of(context).push(
      MaterialPageRoute(builder: (_) => const SettingsScreen()),
    );
    _loadTileKinds(); // pick up any tile choices changed while there
  }

  void _sendAxis() {
    final slider = ref.read(sliderAxisProvider);
    final manualJoystick = ref.read(manualJoystickProvider);
    ref.read(udpClientProvider).sendAxis(proto.Axis(
          pitch: manualJoystick ? _joystick.dy : _attitude.pitch,
          roll: manualJoystick ? _joystick.dx : _attitude.roll,
          throttle: slider.throttle,
          rudder: slider.rudder,
        ));
  }

  void _sendButton(int buttonId, bool pressed) {
    _buttonEventId = (_buttonEventId + 1) & 0xffff;
    ref.read(udpClientProvider).sendButton(proto.Button(
          buttonId: buttonId,
          pressed: pressed,
          eventId: _buttonEventId,
        ));
  }

  Future<void> _tapButton(int buttonId) async {
    _sendButton(buttonId, true);
    await Future.delayed(const Duration(milliseconds: 80));
    _sendButton(buttonId, false);
  }

  void _togglePause() => _tapButton(ButtonIds.pause);

  // There's no SimConnect event to set an exact flap handle index — only
  // FLAPS_INCR/FLAPS_DECR, one detent per event — so [steps] detents of
  // difference becomes [steps] sequential taps, awaited one at a time so
  // each lands as its own press/release BUTTON pair.
  Future<void> _stepFlaps(int steps) async {
    final id = steps > 0 ? ButtonIds.flapIncr : ButtonIds.flapDecr;
    for (var i = 0; i < steps.abs(); i++) {
      await _tapButton(id);
    }
  }

  @override
  void dispose() {
    _sendTimer?.cancel();
    _attitudeSub?.cancel();
    _attitudeStream.dispose();
    WakelockPlus.disable();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final connectionStatus = ref.watch(connectionStatusProvider).valueOrNull;
    final telemetry = ref.watch(telemetryProvider).valueOrNull;
    final manualJoystick = ref.watch(manualJoystickProvider);

    // Pause state comes from the sim's own Pause system event (TELEMETRY),
    // not a locally-guessed toggle — so an external pause/unpause (the Esc
    // key on the PC, another control) shows up correctly here too. Recenter
    // on the real paused->unpaused transition: whatever grip the phone
    // settled into while set down is now "neutral", so resuming shouldn't
    // jolt the aircraft with a stale offset.
    ref.listen(telemetryProvider, (previous, next) {
      final was = previous?.valueOrNull?.paused ?? false;
      final isNow = next.valueOrNull?.paused ?? false;
      if (was && !isNow) _attitudeStream.recenter();
    });
    final isPaused = telemetry?.paused ?? false;

    return Scaffold(
      appBar: AppBar(
        title: const Text('mHOTAS'),
        actions: [
          TextButton.icon(
            icon: const Icon(Icons.center_focus_strong),
            label: const Text('Calibrate'),
            onPressed: _attitudeStream.recenter,
          ),
          IconButton(
            icon: const Icon(Icons.settings),
            tooltip: 'Settings',
            onPressed: _openSettings,
          ),
        ],
      ),
      body: SafeArea(
        child: Stack(
          children: [
            Column(
              children: [
                _ConnectionBanner(status: connectionStatus),
                _TelemetryRow(telemetry: telemetry),
                Expanded(
                  child: Row(
                    children: [
                      Expanded(
                        child: Center(
                          child: manualJoystick
                              ? _ManualJoystick(
                                  onChanged: (v) =>
                                      setState(() => _joystick = v))
                              : _TiltIndicator(attitude: _attitude),
                        ),
                      ),
                      _ThrottleLever(
                        onChanged: (v) => ref
                            .read(sliderAxisProvider.notifier)
                            .setThrottle(v),
                        synced: telemetry?.throttlePercent,
                      ),
                    ],
                  ),
                ),
                Padding(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                  child: Row(
                    mainAxisAlignment: MainAxisAlignment.spaceEvenly,
                    children: [
                      _BigButton(
                          label: 'GEAR',
                          onTap: () => _tapButton(ButtonIds.gear)),
                      _BigButton(
                        label: 'BRAKE',
                        onTap: () => _tapButton(ButtonIds.parkingBrake),
                        active: telemetry?.parkingBrakeSet ?? false,
                      ),
                      _BigButton(
                          label: 'PAUSE',
                          onTap: _togglePause,
                          active: isPaused),
                    ],
                  ),
                ),
              ],
            ),
            Positioned(
              left: 16,
              bottom: 16,
              child: _RudderStick(
                onChanged: (v) =>
                    ref.read(sliderAxisProvider.notifier).setRudder(v),
              ),
            ),
            Positioned(
              left: 16,
              top: 16,
              child: _StatusTilesRow(kinds: _tileKinds, telemetry: telemetry),
            ),
            Positioned(
              right: 16,
              bottom: 16,
              child: _FlapLever(
                onStep: (steps) => _stepFlaps(steps),
                synced: telemetry?.flapHandleIndex,
                max: telemetry?.flapHandleIndexMax,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ConnectionBanner extends StatelessWidget {
  const _ConnectionBanner({required this.status});

  final ConnectionStatus? status;

  @override
  Widget build(BuildContext context) {
    // versionMismatch/error/null all read as "not usable" to the pilot —
    // same as disconnected — so they share its label and color rather than
    // adding states nobody asked to distinguish at a glance.
    final (label, color) = switch (status) {
      ConnectionStatus.connected => ('Connected', Colors.green),
      ConnectionStatus.connecting => ('Connecting', Colors.orange),
      _ => ('Disconnected', Colors.red),
    };
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Text(
        'mHOTAS · $label',
        textAlign: TextAlign.center,
        style: TextStyle(color: color, fontWeight: FontWeight.bold),
      ),
    );
  }
}

class _TelemetryRow extends StatelessWidget {
  const _TelemetryRow({required this.telemetry});

  final proto.Telemetry? telemetry;

  String get _gearLabel {
    switch (telemetry?.gearPosition) {
      case proto.GearPosition.down:
        return 'GEAR DOWN';
      case proto.GearPosition.transit:
        return 'GEAR TRANSIT';
      case proto.GearPosition.up:
        return 'GEAR UP';
      default:
        return 'GEAR --';
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = telemetry;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceEvenly,
        children: [
          Text(t == null
              ? 'FLAP --/--'
              : 'FLAP ${t.flapHandleIndex}/${t.flapHandleIndexMax}'),
          Text(_gearLabel),
        ],
      ),
    );
  }
}

/// 4 configurable status tiles (default: airspeed/altitude/heading/vertical
/// speed) — which simvar each one shows is picked in SettingsScreen and
/// persisted via status_tiles.dart.
class _StatusTilesRow extends StatelessWidget {
  const _StatusTilesRow({required this.kinds, required this.telemetry});

  final List<StatusTileKind> kinds;
  final proto.Telemetry? telemetry;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        for (final kind in kinds)
          Padding(
            padding: const EdgeInsets.only(right: 8),
            child: _StatusTile(kind: kind, telemetry: telemetry),
          ),
      ],
    );
  }
}

class _StatusTile extends StatelessWidget {
  const _StatusTile({required this.kind, required this.telemetry});

  final StatusTileKind kind;
  final proto.Telemetry? telemetry;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      width: 64,
      height: 48,
      decoration: BoxDecoration(
        color: scheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Text(kind.label, style: const TextStyle(fontSize: 10)),
          Text(kind.valueOf(telemetry),
              style:
                  const TextStyle(fontSize: 16, fontWeight: FontWeight.bold)),
        ],
      ),
    );
  }
}

/// A small sanity/calibration aid, not the primary interaction surface —
/// the user feels tilt physically, they don't watch this while flying.
/// Styled as a simple artificial horizon rather than a dot-in-a-box: the
/// sky/ground plane rotates with roll and shifts with pitch behind a fixed
/// aircraft reference, plus a numeric readout for precise calibration checks.
class _TiltIndicator extends StatelessWidget {
  const _TiltIndicator({required this.attitude});

  final Attitude attitude;

  // Matches AttitudeStream's default maxTiltRadians (attitude.dart) — the
  // normalized -1..1 attitude values are full-scale at this angle.
  static const _maxTiltDegrees = 45.0;

  @override
  Widget build(BuildContext context) {
    final pitchDeg = attitude.pitch * _maxTiltDegrees;
    final rollDeg = attitude.roll * _maxTiltDegrees;
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(8),
          child: Container(
            width: 160,
            height: 160,
            decoration: BoxDecoration(
              border: Border.all(color: Colors.grey),
              borderRadius: BorderRadius.circular(8),
            ),
            child: CustomPaint(
              size: const Size(160, 160),
              painter: _HorizonPainter(pitchDeg: pitchDeg, rollDeg: rollDeg),
            ),
          ),
        ),
        const SizedBox(height: 4),
        Text('P: ${pitchDeg.round()}°  R: ${rollDeg.round()}°'),
      ],
    );
  }
}

class _HorizonPainter extends CustomPainter {
  _HorizonPainter({required this.pitchDeg, required this.rollDeg});

  final double pitchDeg;
  final double rollDeg;

  @override
  void paint(Canvas canvas, Size size) {
    final center = size.center(Offset.zero);
    // Nose up shows more sky, so the horizon line moves down on screen as
    // pitch increases; scaled so full deflection puts it at the box edge.
    final pitchOffset =
        pitchDeg / _TiltIndicator._maxTiltDegrees * size.height / 2;
    // Oversized so the rotated sky/ground rectangles still cover the box's
    // corners instead of leaving gaps at max bank.
    final oversize = size.longestSide * 2;

    canvas.save();
    canvas.translate(center.dx, center.dy);
    // Rotated opposite the roll sign: the aircraft reference stays fixed and
    // level while the horizon appears to bank under it, matching a real AH.
    canvas.rotate(-rollDeg * math.pi / 180);
    canvas.drawRect(
      Rect.fromLTWH(-oversize, -oversize + pitchOffset, oversize * 2, oversize),
      Paint()..color = Colors.lightBlue.shade300,
    );
    canvas.drawRect(
      Rect.fromLTWH(-oversize, pitchOffset, oversize * 2, oversize),
      Paint()..color = Colors.brown.shade400,
    );
    canvas.drawLine(
      Offset(-oversize, pitchOffset),
      Offset(oversize, pitchOffset),
      Paint()
        ..color = Colors.white
        ..strokeWidth = 2,
    );
    canvas.restore();

    final aircraftPaint = Paint()
      ..color = Colors.yellow.shade700
      ..strokeWidth = 3
      ..strokeCap = StrokeCap.round;
    canvas.drawLine(Offset(center.dx - 24, center.dy),
        Offset(center.dx - 8, center.dy), aircraftPaint);
    canvas.drawLine(Offset(center.dx + 8, center.dy),
        Offset(center.dx + 24, center.dy), aircraftPaint);
    canvas.drawCircle(center, 3, aircraftPaint);
  }

  @override
  bool shouldRepaint(_HorizonPainter oldDelegate) =>
      oldDelegate.pitchDeg != pitchDeg || oldDelegate.rollDeg != rollDeg;
}

/// Throttle lever: -1 (full reverse) .. 1 (full forward). The forward half
/// (0..1) is a normal free-standing lever — drag it anywhere and it stays,
/// like a real throttle. The reverse half is momentary, like a reverser's
/// spring-loaded safety detent — release below idle and it snaps back to 0
/// rather than staying set, so reverse thrust can never be left on by
/// accident.
///
/// [synced] is the sim's actual current throttle position (from
/// TELEMETRY). While the pilot isn't touching the lever, it mirrors
/// [synced] instead of holding a stale value — so a manual in-sim throttle
/// change shows up here, and the next drag starts from where the throttle
/// really is instead of wherever this widget was last left.
class _ThrottleLever extends StatefulWidget {
  const _ThrottleLever({required this.onChanged, this.synced});

  final ValueChanged<double> onChanged;
  final double? synced;

  @override
  State<_ThrottleLever> createState() => _ThrottleLeverState();
}

class _ThrottleLeverState extends State<_ThrottleLever>
    with SingleTickerProviderStateMixin {
  static const _height = 220.0;
  static const _knobHeight = 40.0;

  double _value = 0; // -1..1
  double _animFrom = 0;
  double _animTo = 0;
  // Whether the in-flight animation should keep calling widget.onChanged as
  // it moves: true for the idle spring-back (it's actively commanding the
  // sim toward idle), false for catching up to a synced value (that's just
  // this widget visually following what the sim already reports — calling
  // onChanged with in-between values there would re-send throttle numbers
  // the sim never asked for).
  bool _animNotifies = true;
  bool _dragging = false;
  late final AnimationController _anim;

  // Timestamp of the last time this widget actively commanded a value
  // (drag or the notify-driven idle spring-back). SimConnect only reports
  // throttle position at ~1Hz, so right after we command a new value —
  // e.g. releasing in the reverse zone, which should snap straight to idle
  // — the last known `synced` reading is still the *pre-release* value for
  // up to a second. Without this guard, the very next rebuild's
  // didUpdateWidget would see that stale synced value, decide the lever
  // needs to "catch up", and drag it right back into reverse — which is
  // why releasing used to require overshooting into positive first to
  // eventually win the race. Comfortably longer than the telemetry period.
  DateTime _lastLocalCommandAt = DateTime.fromMillisecondsSinceEpoch(0);
  static const _localCommandGrace = Duration(milliseconds: 1200);

  @override
  void initState() {
    super.initState();
    _anim = AnimationController(vsync: this)
      ..addListener(() {
        setState(
            () => _value = _animFrom + (_animTo - _animFrom) * _anim.value);
        if (_animNotifies) _notifyChanged();
      });
  }

  void _notifyChanged() {
    _lastLocalCommandAt = DateTime.now();
    widget.onChanged(_value);
  }

  void _animateTo(double target,
      {required bool notify, required Duration duration}) {
    _animFrom = _value;
    _animTo = target;
    _animNotifies = notify;
    _anim.duration = duration;
    _anim.forward(from: 0);
  }

  @override
  void didUpdateWidget(covariant _ThrottleLever oldWidget) {
    super.didUpdateWidget(oldWidget);
    final synced = widget.synced;
    final recentlyCommanded =
        DateTime.now().difference(_lastLocalCommandAt) < _localCommandGrace;
    // Ease toward a new synced reading instead of snapping instantly —
    // telemetry only arrives ~1x/sec, so an instant jump reads as a visible
    // stutter; animating over most of that interval reads as smooth
    // tracking instead. Compares against _animTo (the in-flight target),
    // not _value, so repeated rebuilds with the same synced reading don't
    // keep restarting the animation from scratch.
    if (!_dragging &&
        !recentlyCommanded &&
        synced != null &&
        synced != _animTo) {
      _animateTo(synced,
          notify: false, duration: const Duration(milliseconds: 700));
    }
  }

  void _onDragStart(DragStartDetails details) {
    _dragging = true;
    _anim.stop();
  }

  void _onDragUpdate(DragUpdateDetails details) {
    setState(() {
      // Dragging up (negative dy) increases throttle.
      _value = (_value - details.delta.dy / (_height / 2)).clamp(-1.0, 1.0);
    });
    _notifyChanged();
  }

  // Below this, a release snaps to exact idle instead of holding wherever
  // the finger happened to stop — real fingers can't land on an exact
  // pixel, and idle needs to be reliably reachable, not "close to zero."
  static const _idleSnapBand = 0.03;

  void _onDragEnd(DragEndDetails details) {
    _dragging = false;
    if (_value < _idleSnapBand) {
      _animateTo(0, notify: true, duration: const Duration(milliseconds: 150));
    }
  }

  @override
  void dispose() {
    _anim.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return GestureDetector(
      onVerticalDragStart: _onDragStart,
      onVerticalDragUpdate: _onDragUpdate,
      onVerticalDragEnd: _onDragEnd,
      child: Container(
        width: 64,
        height: _height,
        decoration: BoxDecoration(
          color: scheme.surfaceContainerHighest,
          borderRadius: BorderRadius.circular(12),
        ),
        child: Stack(
          alignment: Alignment.center,
          children: [
            const Positioned(
                top: 6, child: Text('THR', style: TextStyle(fontSize: 11))),
            // Idle-line marker: reverse zone is below this.
            Container(height: 1, color: scheme.outline),
            Align(
              alignment: Alignment(
                  0, -_value), // y: -1 = top (full fwd), 1 = bottom (full rev)
              child: Container(
                width: 52,
                height: _knobHeight,
                margin: const EdgeInsets.symmetric(horizontal: 4),
                decoration: BoxDecoration(
                  color: _value < 0 ? scheme.error : scheme.primary,
                  borderRadius: BorderRadius.circular(8),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// Flap lever: discrete detents 0..[max] (from TELEMETRY's
/// flapHandleIndexMax), unlike the throttle's continuous drag — a real flap
/// handle only has fixed notches, and SimConnect only exposes
/// FLAPS_INCR/FLAPS_DECR (step one detent at a time), not a set-to-index
/// event, so dragging to a new detent and releasing sends that many INCR
/// or DECR BUTTON taps rather than one absolute value.
///
/// [synced] is the sim's actual current flap handle index (from
/// TELEMETRY) — same don't-fight-the-sim mirroring _ThrottleLever uses:
/// while not dragging, the lever shows [synced] instead of a stale local
/// guess, so an in-sim flap change shows up here too.
class _FlapLever extends StatefulWidget {
  const _FlapLever(
      {required this.onStep, required this.synced, required this.max});

  /// Requests [steps] detents of movement: positive = FLAPS_INCR taps,
  /// negative = FLAPS_DECR taps.
  final ValueChanged<int> onStep;
  final int? synced;
  final int? max;

  @override
  State<_FlapLever> createState() => _FlapLeverState();
}

class _FlapLeverState extends State<_FlapLever> {
  static const _height = 160.0;
  static const _knobHeight = 32.0;

  bool _dragging = false;
  double _dragFraction = 0; // 0 (up/retracted) .. 1 (down/full flaps)
  int? _pendingDetent;

  // Same rationale as _ThrottleLever._localCommandGrace: telemetry only
  // arrives ~1x/sec, so right after a tap the last known `synced` reading
  // is still the pre-tap value for up to a second — without this guard the
  // lever would visually snap back to the old detent and then jump forward
  // once telemetry catches up.
  DateTime _lastLocalCommandAt = DateTime.fromMillisecondsSinceEpoch(0);
  static const _localCommandGrace = Duration(milliseconds: 1200);

  int get _max => widget.max ?? 0;

  double _fractionFor(int index) => _max == 0 ? 0 : index / _max;

  void _onDragStart(DragStartDetails details) {
    _dragging = true;
    _dragFraction = _fractionFor(_pendingDetent ?? widget.synced ?? 0);
  }

  void _onDragUpdate(DragUpdateDetails details) {
    setState(() {
      _dragFraction =
          (_dragFraction + details.delta.dy / _height).clamp(0.0, 1.0);
    });
  }

  void _onDragEnd(DragEndDetails details) {
    if (_max > 0) {
      final target = (_dragFraction * _max).round().clamp(0, _max);
      final current = widget.synced ?? _pendingDetent ?? 0;
      final steps = target - current;
      if (steps != 0) widget.onStep(steps);
      _pendingDetent = target;
      _lastLocalCommandAt = DateTime.now();
    }
    setState(() => _dragging = false);
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final recentlyCommanded =
        DateTime.now().difference(_lastLocalCommandAt) < _localCommandGrace;
    final displayIndex =
        recentlyCommanded ? (_pendingDetent ?? widget.synced) : widget.synced;
    final fraction =
        _dragging ? _dragFraction : _fractionFor(displayIndex ?? 0);

    return GestureDetector(
      onVerticalDragStart: _onDragStart,
      onVerticalDragUpdate: _onDragUpdate,
      onVerticalDragEnd: _onDragEnd,
      child: Container(
        width: 56,
        height: _height,
        decoration: BoxDecoration(
          color: scheme.surfaceContainerHighest,
          borderRadius: BorderRadius.circular(12),
        ),
        child: Stack(
          alignment: Alignment.center,
          children: [
            const Positioned(
                top: 6, child: Text('FLAP', style: TextStyle(fontSize: 11))),
            Positioned(
              bottom: 4,
              child: Text(
                widget.max == null
                    ? '--/--'
                    : '${displayIndex ?? 0}/${widget.max}',
                style: const TextStyle(fontSize: 10),
              ),
            ),
            AnimatedAlign(
              duration: const Duration(milliseconds: 200),
              alignment: Alignment(0, fraction * 2 - 1),
              child: Container(
                width: 44,
                height: _knobHeight,
                margin: const EdgeInsets.symmetric(horizontal: 4),
                decoration: BoxDecoration(
                  color: scheme.primary,
                  borderRadius: BorderRadius.circular(8),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// Spring-loaded rudder control: drag left/right, release and it snaps
/// back to neutral — unlike throttle, rudder shouldn't hold a deflection
/// once you let go, matching physical rudder pedals.
class _RudderStick extends StatefulWidget {
  const _RudderStick({required this.onChanged});

  final ValueChanged<double> onChanged;

  @override
  State<_RudderStick> createState() => _RudderStickState();
}

class _RudderStickState extends State<_RudderStick>
    with SingleTickerProviderStateMixin {
  static const _width = 160.0;
  static const _knobWidth = 44.0;

  double _value = 0; // -1..1
  double _springBackFrom = 0;
  late final AnimationController _springBack;

  @override
  void initState() {
    super.initState();
    _springBack = AnimationController(
        vsync: this, duration: const Duration(milliseconds: 150))
      ..addListener(() {
        setState(() => _value = _springBackFrom * (1 - _springBack.value));
        widget.onChanged(_value);
      });
  }

  void _onDragUpdate(DragUpdateDetails details) {
    _springBack.stop();
    setState(() {
      _value = (_value + details.delta.dx / (_width / 2)).clamp(-1.0, 1.0);
    });
    widget.onChanged(_value);
  }

  void _onDragEnd(DragEndDetails details) {
    _springBackFrom = _value;
    _springBack.forward(from: 0);
  }

  @override
  void dispose() {
    _springBack.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return GestureDetector(
      onHorizontalDragUpdate: _onDragUpdate,
      onHorizontalDragEnd: _onDragEnd,
      child: Container(
        width: _width,
        height: 56,
        decoration: BoxDecoration(
          color: scheme.surfaceContainerHighest,
          borderRadius: BorderRadius.circular(12),
        ),
        child: Stack(
          alignment: Alignment.center,
          children: [
            const Text('RUD'),
            Align(
              alignment: Alignment(_value, 0),
              child: Container(
                width: _knobWidth,
                height: 48,
                margin: const EdgeInsets.all(4),
                decoration: BoxDecoration(
                    color: scheme.primary,
                    borderRadius: BorderRadius.circular(8)),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// Settings-gated 2D pitch/roll input, replacing the tilt sensor as the
/// AXIS pitch/roll source while enabled (see settings_screen.dart and
/// _ControlScreenState._sendAxis). Same spring-back drag pattern as
/// _RudderStick, extended from one axis to two.
class _ManualJoystick extends StatefulWidget {
  const _ManualJoystick({required this.onChanged});

  final ValueChanged<Offset> onChanged; // dx/dy each -1..1

  @override
  State<_ManualJoystick> createState() => _ManualJoystickState();
}

class _ManualJoystickState extends State<_ManualJoystick>
    with SingleTickerProviderStateMixin {
  static const _size = 160.0;
  static const _knobSize = 40.0;

  Offset _value = Offset.zero; // dx/dy each -1..1
  Offset _springBackFrom = Offset.zero;
  late final AnimationController _springBack;

  @override
  void initState() {
    super.initState();
    _springBack = AnimationController(
        vsync: this, duration: const Duration(milliseconds: 150))
      ..addListener(() {
        setState(() => _value = _springBackFrom * (1 - _springBack.value));
        widget.onChanged(_value);
      });
  }

  void _onDragUpdate(DragUpdateDetails details) {
    _springBack.stop();
    setState(() {
      _value = Offset(
        (_value.dx + details.delta.dx / (_size / 2)).clamp(-1.0, 1.0),
        (_value.dy + details.delta.dy / (_size / 2)).clamp(-1.0, 1.0),
      );
    });
    widget.onChanged(_value);
  }

  void _onDragEnd(DragEndDetails details) {
    _springBackFrom = _value;
    _springBack.forward(from: 0);
  }

  @override
  void dispose() {
    _springBack.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return GestureDetector(
      onPanUpdate: _onDragUpdate,
      onPanEnd: _onDragEnd,
      child: Container(
        width: _size,
        height: _size,
        decoration: BoxDecoration(
          color: scheme.surfaceContainerHighest,
          shape: BoxShape.circle,
          border: Border.all(color: scheme.outline),
        ),
        child: Align(
          alignment: Alignment(_value.dx, _value.dy),
          child: Container(
            width: _knobSize,
            height: _knobSize,
            decoration:
                BoxDecoration(color: scheme.primary, shape: BoxShape.circle),
          ),
        ),
      ),
    );
  }
}

/// [active] reflects the sim's actual current state for this button (e.g.
/// TELEMETRY.parkingBrakeSet/.paused) where one exists, not just whether it
/// was last tapped — so an external change (another control, the sim's own
/// keyboard shortcuts) shows up correctly instead of the button lying
/// about what's really engaged.
class _BigButton extends StatelessWidget {
  const _BigButton(
      {required this.label, required this.onTap, this.active = false});

  final String label;
  final VoidCallback onTap;
  final bool active;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 120,
      height: 56,
      child: active
          ? FilledButton(onPressed: onTap, child: Text(label))
          : FilledButton.tonal(onPressed: onTap, child: Text(label)),
    );
  }
}
