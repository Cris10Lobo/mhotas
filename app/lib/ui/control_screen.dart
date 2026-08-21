import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:wakelock_plus/wakelock_plus.dart';

import '../network/protocol.dart' as proto;
import '../network/udp_client.dart';
import '../sensors/attitude.dart';
import '../state/connection.dart';
import '../state/layout.dart';
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
  static const int toeBrake = 6;
  static const int spoilerIncr = 7;
  static const int spoilerDecr = 8;
  static const int spoilerArm = 9;
}

/// The primary flying screen, laid out to match docs/mhotas-mobile.png:
/// tilt the phone (or the optional on-screen joystick) for pitch/roll,
/// levers/sticks for throttle/rudder/flaps/speedbrake, and each control
/// carries its own state (flap lever shows its own position, gear/parking
/// brake are toggles that light up from telemetry) rather than a separate
/// plain-text status readout — controls are sized for occasional glances,
/// not sustained attention.
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

  // No spoilersArmed TELEMETRY field exists yet (that would be a protocol
  // change, out of scope for this milestone), so — unlike the pause/parking
  // brake buttons — there's nothing to read the real state back from; this
  // button just remembers its own last-toggled state, same as the server's
  // Backend.spoilerArmed.
  //
  // ponytail: this and the server's copy can drift (restart either side, or
  // drop all 3 redundant BUTTON packets) with nothing to reconcile them —
  // add a spoilersArmed TELEMETRY field if that turns out to matter live.
  bool _speedbrakeArmed = false;

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

  // One INCR/DECR tap per detent crossed, same discrete-step reasoning as
  // the lever widget itself: SimConnect has no exact-detent "set spoiler
  // position" event, only step events.
  Future<void> _stepSpeedbrake(int delta) async {
    final id = delta > 0 ? ButtonIds.spoilerIncr : ButtonIds.spoilerDecr;
    for (var i = 0; i < delta.abs(); i++) {
      await _tapButton(id);
    }
  }

  void _toggleSpeedbrakeArm() {
    setState(() => _speedbrakeArmed = !_speedbrakeArmed);
    _tapButton(ButtonIds.spoilerArm);
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
    final layoutEditMode = ref.watch(layoutEditModeProvider);

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
      // No separate "mHOTAS" heading — the status text below already carries
      // the name (see _ConnectionStatusText's own note on that), so a
      // second static label next to it was pure redundancy on an already
      // tight landscape app bar.
      appBar: AppBar(
        centerTitle:
            false, // left — centerTitle otherwise defaults per-platform
        title: _ConnectionStatusText(status: connectionStatus),
        actions: [
          TextButton.icon(
            icon: const Icon(Icons.center_focus_strong),
            label: const Text('Calibrate'),
            onPressed: _attitudeStream.recenter,
          ),
          if (layoutEditMode)
            IconButton(
              icon: const Icon(Icons.restore),
              tooltip: 'Reset layout',
              onPressed: () => ref.read(layoutOffsetsProvider.notifier).reset(),
            ),
          IconButton(
            icon: Icon(layoutEditMode ? Icons.lock_open : Icons.open_with),
            tooltip: layoutEditMode ? 'Lock layout' : 'Edit layout',
            color:
                layoutEditMode ? Theme.of(context).colorScheme.primary : null,
            onPressed: () => ref.read(layoutEditModeProvider.notifier).state =
                !layoutEditMode,
          ),
          IconButton(
            icon: const Icon(Icons.settings),
            tooltip: 'Settings',
            onPressed: _openSettings,
          ),
        ],
        // Pause centered across the whole app bar (not just the leftover
        // space between title/actions, which flexibleSpace's full-width
        // Stack sidesteps) — called out by itself since it's the one action
        // an in-flight tap can't afford to miss among the others.
        flexibleSpace: SafeArea(
          child: Center(
            child: IconButton(
              icon: Icon(isPaused ? Icons.play_arrow : Icons.pause),
              tooltip: 'Pause',
              color: isPaused ? Theme.of(context).colorScheme.primary : null,
              onPressed: _togglePause,
            ),
          ),
        ),
      ),
      body: SafeArea(
        child: Stack(
          children: [
            Column(
              children: [
                Align(
                  alignment: Alignment.centerLeft,
                  child: Padding(
                    padding: const EdgeInsets.only(left: 16),
                    child: _Movable(
                      id: 'statusTiles',
                      child: _StatusTilesRow(
                          kinds: _tileKinds, telemetry: telemetry),
                    ),
                  ),
                ),
                Expanded(
                  child: Row(
                    children: [
                      Expanded(
                        child: Center(
                          child: _Movable(
                            id: 'attitude',
                            child: manualJoystick
                                ? _ManualJoystick(
                                    onChanged: (v) =>
                                        setState(() => _joystick = v))
                                : _TiltIndicator(attitude: _attitude),
                          ),
                        ),
                      ),
                      Padding(
                        padding: const EdgeInsets.only(right: 8),
                        child: _Movable(
                          id: 'speedbrake',
                          child: _ControlLabel(
                            label: 'SPEEDBRAKE',
                            child: Column(
                              mainAxisAlignment: MainAxisAlignment.center,
                              children: [
                                _BigButton(
                                  label: 'ARM',
                                  onTap: _toggleSpeedbrakeArm,
                                  active: _speedbrakeArmed,
                                  compact: true,
                                ),
                                const SizedBox(height: 8),
                                _SpeedbrakeLever(onStep: _stepSpeedbrake),
                              ],
                            ),
                          ),
                        ),
                      ),
                      Padding(
                        padding: const EdgeInsets.only(right: 8),
                        child: _Movable(
                          id: 'flapLever',
                          child: _FlapLever(
                            onStep: (steps) => _stepFlaps(steps),
                            synced: telemetry?.flapHandleIndex,
                            max: telemetry?.flapHandleIndexMax,
                          ),
                        ),
                      ),
                      Padding(
                        padding: const EdgeInsets.only(right: 8),
                        child: _Movable(
                          id: 'toeBrake',
                          child: _ControlLabel(
                            label: 'TOE BRAKE',
                            child: _ToeBrake(
                              onPressedChanged: (pressed) =>
                                  _sendButton(ButtonIds.toeBrake, pressed),
                            ),
                          ),
                        ),
                      ),
                      Padding(
                        padding: const EdgeInsets.only(right: 8),
                        child: _Movable(
                          id: 'parkingBrake',
                          child: _ControlLabel(
                            label: 'PARK BRAKE',
                            child: _ParkingBrakeButton(
                              active: telemetry?.parkingBrakeSet ?? false,
                              onTap: () => _tapButton(ButtonIds.parkingBrake),
                            ),
                          ),
                        ),
                      ),
                      Padding(
                        padding: const EdgeInsets.only(right: 8),
                        child: _Movable(
                          id: 'gear',
                          child: _ControlLabel(
                            label: 'GEAR',
                            child: _GearToggle(
                              position: telemetry?.gearPosition,
                              onTap: () => _tapButton(ButtonIds.gear),
                            ),
                          ),
                        ),
                      ),
                      _Movable(
                        id: 'throttle',
                        child: _ControlLabel(
                          label: 'THROTTLE',
                          child: _ThrottleLever(
                            onChanged: (v) => ref
                                .read(sliderAxisProvider.notifier)
                                .setThrottle(v),
                            synced: telemetry?.throttlePercent,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ),
            Positioned(
              left: 16,
              bottom: 16,
              child: _Movable(
                id: 'rudder',
                child: _ControlLabel(
                  label: 'RUDDER',
                  horizontal: true,
                  child: _RudderStick(
                    onChanged: (v) =>
                        ref.read(sliderAxisProvider.notifier).setRudder(v),
                  ),
                ),
              ),
            ),
            // bottom, not top: the status tiles row sits flush at the top of
            // the Column with no clearance, so a top-pinned hint painted
            // over it there — caught live via a real-device screenshot.
            if (layoutEditMode)
              const Positioned(
                bottom: 8,
                left: 0,
                right: 0,
                child: IgnorePointer(
                  child: Center(
                    child: Text(
                      'Drag a control\'s corner handle to reposition it · tap the layout icon to lock',
                      style: TextStyle(fontSize: 12),
                    ),
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

/// Wraps a control so it can be dragged to a custom position while
/// [layoutEditModeProvider] is on — normal flow/flex layout (Row/Column)
/// is untouched underneath; this only applies a paint-time offset on top,
/// so the common case (no offset saved for this id) renders pixel-
/// identical to before free-position mode existed.
///
/// Dragging happens only via a small corner handle — like the grip tab
/// drag-and-drop UIs elsewhere use — not by grabbing anywhere on the
/// control itself. An earlier version made the whole control one big drag
/// surface (with its own gestures suppressed via IgnorePointer so a drag
/// didn't also fire a button/lever), but that made compound controls (a
/// lever plus a button, say) fiddly to grab without missing the intended
/// target — reported live. The control underneath now keeps working
/// completely normally, in or out of edit mode; only the handle differs.
class _Movable extends ConsumerWidget {
  const _Movable({required this.id, required this.child});

  final String id;
  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final editMode = ref.watch(layoutEditModeProvider);
    final offset =
        ref.watch(layoutOffsetsProvider.select((m) => m[id])) ?? Offset.zero;

    Widget content = child;
    if (editMode) {
      final scheme = Theme.of(context).colorScheme;
      content = Stack(
        clipBehavior: Clip.none,
        children: [
          DecoratedBox(
            decoration: BoxDecoration(
              border: Border.all(color: scheme.primary, width: 2),
              borderRadius: BorderRadius.circular(8),
            ),
            child: child,
          ),
          // top:0/right:0, not negative: Clip.none only lets a Positioned
          // child *paint* outside the Stack's box, it doesn't extend hit
          // testing there — a parent (Row/Padding) still gates on "inside my
          // own reported size" before ever reaching this child, so a
          // negative offset left ~3/4 of the visible circle untouchable
          // (reported live as "can't drag some of the components"). Sitting
          // flush at the corner keeps the whole handle inside the box that's
          // actually hit-tested.
          Positioned(
            top: 0,
            right: 0,
            child: GestureDetector(
              onPanUpdate: (details) => ref
                  .read(layoutOffsetsProvider.notifier)
                  .addDelta(id, details.delta),
              child: Container(
                width: 28,
                height: 28,
                decoration: BoxDecoration(
                    color: scheme.primary, shape: BoxShape.circle),
                child: Icon(Icons.drag_indicator,
                    size: 16, color: scheme.onPrimary),
              ),
            ),
          ),
        ],
      );
    }
    return Transform.translate(offset: offset, child: content);
  }
}

/// Small caption above a control, positioned to match the wireframe's own
/// convention: centered above a compact/vertical control (a lever, a
/// circular button), left-aligned above a wide horizontal one (the rudder
/// bar) rather than centered over mostly-empty space either side. Kept
/// small and low-contrast on purpose — a glanceable name for the control,
/// not something competing with the control itself for attention.
class _ControlLabel extends StatelessWidget {
  const _ControlLabel({
    required this.label,
    required this.child,
    this.horizontal = false,
  });

  final String label;
  final Widget child;
  final bool horizontal;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment:
          horizontal ? CrossAxisAlignment.start : CrossAxisAlignment.center,
      children: [
        Padding(
          padding: const EdgeInsets.only(bottom: 2),
          child: Text(
            label,
            style: TextStyle(
              fontSize: 10,
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          ),
        ),
        child,
      ],
    );
  }
}

/// AppBar title — used as the title itself, not a separate corner
/// annotation, per the wireframe: the app's name lives in the color-coded
/// connection state, not as a second static "mHOTAS" heading next to it.
class _ConnectionStatusText extends StatelessWidget {
  const _ConnectionStatusText({required this.status});

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
    return Text(
      'mHOTAS - $label',
      style: TextStyle(color: color, fontWeight: FontWeight.bold, fontSize: 12),
    );
  }
}

/// Compact UP/DOWN toggle switch for the landing gear, per the wireframe —
/// replaces a plain "GEAR" button + separate "GEAR UP/DOWN/TRANSIT" text
/// readout with one widget that's both the control and its own indicator.
class _GearToggle extends StatelessWidget {
  const _GearToggle({required this.position, required this.onTap});

  final int? position;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final down = position == proto.GearPosition.down;
    final transit = position == proto.GearPosition.transit;
    final thumbAlign = down
        ? Alignment.bottomCenter
        : (transit ? Alignment.center : Alignment.topCenter);
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: 40,
        height: 88,
        padding: const EdgeInsets.symmetric(vertical: 6),
        decoration: BoxDecoration(
          color: scheme.surfaceContainerHighest,
          borderRadius: BorderRadius.circular(20),
        ),
        child: Column(
          children: [
            Text('UP',
                style: TextStyle(
                    fontSize: 9,
                    fontWeight: !down && !transit
                        ? FontWeight.bold
                        : FontWeight.normal)),
            Expanded(
              child: AnimatedAlign(
                duration: const Duration(milliseconds: 200),
                alignment: thumbAlign,
                child: Container(
                  width: 28,
                  height: 28,
                  decoration: BoxDecoration(
                    shape: BoxShape.circle,
                    color: transit ? scheme.tertiary : scheme.primary,
                  ),
                ),
              ),
            ),
            Text('DOWN',
                style: TextStyle(
                    fontSize: 9,
                    fontWeight: down ? FontWeight.bold : FontWeight.normal)),
          ],
        ),
      ),
    );
  }
}

/// Parking brake, styled as the wireframe's "Parking break icon" circle —
/// same circular treatment as _ToeBrake, but a tap-toggle (parking brake
/// holds until tapped again) rather than a hold-while-pressed control.
class _ParkingBrakeButton extends StatelessWidget {
  const _ParkingBrakeButton({required this.active, required this.onTap});

  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return GestureDetector(
      onTap: onTap,
      child: Container(
        width: 72,
        height: 72,
        decoration: BoxDecoration(
          color: active ? scheme.primary : scheme.surfaceContainerHighest,
          shape: BoxShape.circle,
        ),
        alignment: Alignment.center,
        child: Icon(Icons.local_parking,
            color: active ? scheme.onPrimary : scheme.onSurface),
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

  // Single source of truth for "what detent are we at right now" — used by
  // drag-start (to seed the drag) and drag-end (to compute the step delta)
  // alike, so both agree with what build() is showing. Using widget.synced
  // directly in either of those (as opposed to this grace-guarded value)
  // reintroduces the stale-telemetry race the grace window exists to avoid:
  // a second drag within _localCommandGrace of the first would diff against
  // the pre-tap synced value and double-send the already-taken steps.
  int _currentDetent() {
    final recentlyCommanded =
        DateTime.now().difference(_lastLocalCommandAt) < _localCommandGrace;
    return (recentlyCommanded ? _pendingDetent : null) ?? widget.synced ?? 0;
  }

  void _onDragStart(DragStartDetails details) {
    _dragging = true;
    _dragFraction = _fractionFor(_currentDetent());
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
      final steps = target - _currentDetent();
      if (steps != 0) widget.onStep(steps);
      _pendingDetent = target;
      _lastLocalCommandAt = DateTime.now();
    }
    setState(() => _dragging = false);
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final displayIndex = _currentDetent();
    final fraction = _dragging ? _dragFraction : _fractionFor(displayIndex);

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
                widget.max == null ? '--/--' : '$displayIndex/${widget.max}',
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
      {required this.label,
      required this.onTap,
      this.active = false,
      this.compact = false});

  final String label;
  final VoidCallback onTap;
  final bool active;
  // Shrinks to a small toggle indicator (the spoiler ARM button) instead of
  // the main button row's usual glanceable size.
  final bool compact;

  // Material 3's default FilledButton padding (~24px each side) alone
  // exceeds the compact box's whole 64px width, forcing even a 3-letter
  // label like "ARM" to wrap — reported live. Tight padding + no enforced
  // minimum tap-target size so the button actually fits the SizedBox
  // instead of fighting it.
  static const _compactStyle = ButtonStyle(
    padding: WidgetStatePropertyAll(EdgeInsets.symmetric(horizontal: 4)),
    minimumSize: WidgetStatePropertyAll(Size.zero),
    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
  );

  @override
  Widget build(BuildContext context) {
    final child = Text(
      label,
      style: compact ? const TextStyle(fontSize: 11) : null,
      softWrap: false,
      overflow: TextOverflow.visible,
    );
    final style = compact ? _compactStyle : null;
    return SizedBox(
      width: compact ? 64 : 120,
      height: compact ? 36 : 56,
      child: active
          ? FilledButton(onPressed: onTap, style: style, child: child)
          : FilledButton.tonal(onPressed: onTap, style: style, child: child),
    );
  }
}

/// Speedbrake/spoiler lever: a small number of discrete detents (0..2, per
/// the wireframe's "2/1/0" labels) rather than a continuous drag-to-target
/// — SimConnect has no exact-detent "set spoiler position" event
/// (SPOILERS_INC/SPOILERS_DEC only step it, same limitation as flaps), so
/// dragging across a detent boundary sends one BUTTON tap per step instead
/// of an absolute value. Up = more deployed, matching the throttle lever's
/// up-is-more convention.
///
/// Unlike the throttle lever, there is no TELEMETRY field for spoiler
/// handle position (adding one would be a protocol change, out of scope
/// for this milestone), so this widget's position is purely local — it
/// can't mirror an in-sim spoiler change the way the throttle lever mirrors
/// throttlePercent.
class _SpeedbrakeLever extends StatefulWidget {
  const _SpeedbrakeLever({required this.onStep});

  /// Called with the number of detents moved: positive for SPOILERS_INC
  /// taps, negative for SPOILERS_DEC taps.
  final ValueChanged<int> onStep;

  @override
  State<_SpeedbrakeLever> createState() => _SpeedbrakeLeverState();
}

class _SpeedbrakeLeverState extends State<_SpeedbrakeLever> {
  static const _detentMax = 2; // detents 0 (retracted) .. 2 (full deploy)
  static const _height = 160.0;
  static const _knobHeight = 32.0;

  double _dragValue = 0; // fractional position while dragging, 0.._detentMax
  int _detent = 0;

  void _onDragStart(DragStartDetails details) {
    _dragValue = _detent.toDouble();
  }

  void _onDragUpdate(DragUpdateDetails details) {
    setState(() {
      _dragValue = (_dragValue - details.delta.dy / (_height / _detentMax))
          .clamp(0.0, _detentMax.toDouble());
    });
    final newDetent = _dragValue.round();
    if (newDetent != _detent) {
      widget.onStep(newDetent - _detent);
      _detent = newDetent;
    }
  }

  void _onDragEnd(DragEndDetails details) {
    setState(() => _dragValue = _detent.toDouble()); // snap knob to the detent
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
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
                top: 6, child: Text('SPD', style: TextStyle(fontSize: 11))),
            const Positioned(
              right: 4,
              top: 24,
              bottom: 8,
              child: Column(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  Text('2', style: TextStyle(fontSize: 10)),
                  Text('1', style: TextStyle(fontSize: 10)),
                  Text('0', style: TextStyle(fontSize: 10)),
                ],
              ),
            ),
            Align(
              // y: -1 = top (full deploy), 1 = bottom (retracted).
              alignment: Alignment(0, 1 - (_dragValue / _detentMax) * 2),
              child: Container(
                width: 40,
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

/// Toe brake: tap-and-hold, not a slider. AXIS_LEFT_BRAKE_SET/
/// AXIS_RIGHT_BRAKE_SET are documented as non-linear and asymmetric to each
/// other, so there's no usable proportional "brake X%" — this only ever
/// sends full-apply on press and full-release on lift. Note: if the sim's
/// parking brake is engaged, holding this may show no visible effect —
/// that's the parking-brake interlock, not a bug here.
class _ToeBrake extends StatefulWidget {
  const _ToeBrake({required this.onPressedChanged});

  final ValueChanged<bool> onPressedChanged;

  @override
  State<_ToeBrake> createState() => _ToeBrakeState();
}

class _ToeBrakeState extends State<_ToeBrake> {
  bool _pressed = false;

  void _setPressed(bool pressed) {
    if (_pressed == pressed) return;
    setState(() => _pressed = pressed);
    widget.onPressedChanged(pressed);
  }

  @override
  void dispose() {
    // Backgrounding/navigating away mid-hold doesn't fire onTapCancel — send
    // the release so the server doesn't hold the brake applied forever.
    if (_pressed) widget.onPressedChanged(false);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return GestureDetector(
      onTapDown: (_) => _setPressed(true),
      onTapUp: (_) => _setPressed(false),
      onTapCancel: () => _setPressed(false),
      child: Container(
        width: 72,
        height: 72,
        decoration: BoxDecoration(
          color: _pressed ? scheme.error : scheme.surfaceContainerHighest,
          shape: BoxShape.circle,
        ),
        alignment: Alignment.center,
        child: Text('BRK',
            style:
                TextStyle(color: _pressed ? scheme.onError : scheme.onSurface)),
      ),
    );
  }
}
