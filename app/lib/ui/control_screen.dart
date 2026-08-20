import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:wakelock_plus/wakelock_plus.dart';

import '../network/protocol.dart' as proto;
import '../network/udp_client.dart';
import '../sensors/attitude.dart';
import '../state/connection.dart';
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
    ref.read(udpClientProvider).sendAxis(proto.Axis(
          pitch: _attitude.pitch,
          roll: _attitude.roll,
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
          IconButton(
            icon: const Icon(Icons.center_focus_strong),
            tooltip: 'Recenter tilt',
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
                              child: _TiltIndicator(attitude: _attitude))),
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
    final connected = status == ConnectionStatus.connected;
    return Container(
      width: double.infinity,
      color: connected ? Colors.green.shade700 : Colors.red.shade700,
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Text(
        connected ? 'Connected' : 'Connection lost',
        textAlign: TextAlign.center,
        style: const TextStyle(color: Colors.white),
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
class _TiltIndicator extends StatelessWidget {
  const _TiltIndicator({required this.attitude});

  final Attitude attitude;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 160,
      height: 160,
      decoration: BoxDecoration(
        border: Border.all(color: Colors.grey),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Align(
        alignment: Alignment(
          attitude.roll.clamp(-1, 1),
          attitude.pitch.clamp(-1, 1),
        ),
        child: Container(
          width: 20,
          height: 20,
          decoration:
              const BoxDecoration(color: Colors.blue, shape: BoxShape.circle),
        ),
      ),
    );
  }
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
