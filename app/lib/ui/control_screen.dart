import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:vibration/vibration.dart';
import 'package:wakelock_plus/wakelock_plus.dart';

import '../network/protocol.dart' as proto;
import '../network/udp_client.dart';
import '../sensors/attitude.dart';
import '../state/connection.dart';
import '../state/gyro_pause.dart';
import '../state/layout.dart';
import '../state/settings.dart';
import '../state/slider_axis.dart';
import '../state/telemetry.dart';
import 'brand.dart';
import 'settings_screen.dart';

/// Must match the button IDs in the server config.
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
  static const int autopilotDisconnect = 10;
  static const int trimUp = 11;
  static const int trimDown = 12;
  static const int throttleIncr = 13;
  static const int throttleDecr = 14;
  static const int throttleReverseArm = 15;
}

class ControlScreen extends ConsumerStatefulWidget {
  const ControlScreen({super.key});

  @override
  ConsumerState<ControlScreen> createState() => _ControlScreenState();
}

class _ControlScreenState extends ConsumerState<ControlScreen> {
  final _attitudeStream = AttitudeStream();
  Attitude _attitude = const Attitude(pitch: 0, roll: 0);
  Offset _joystick = Offset.zero;
  StreamSubscription<Attitude>? _attitudeSub;
  Timer? _sendTimer;
  int _buttonEventId = 0;

  bool _stallShaking = false;
  int _taxiRumbleBand = -1; // -1 = off

  @override
  void initState() {
    super.initState();
    // Fails without a platform binding (headless tests).
    WakelockPlus.enable().catchError((_) {});
    _attitudeStream.start();
    _attitudeSub = _attitudeStream.stream.listen((a) {
      if (mounted) setState(() => _attitude = a);
    });
    _sendTimer =
        Timer.periodic(const Duration(milliseconds: 33), (_) => _sendAxis());
  }

  Future<void> _openSettings() async {
    await Navigator.of(context).push(
      MaterialPageRoute(builder: (_) => const SettingsScreen()),
    );
  }

  void _sendAxis() {
    final slider = ref.read(sliderAxisProvider);
    final manualJoystick = ref.read(manualJoystickProvider);
    final gyroPaused = ref.read(gyroPausedProvider);
    ref.read(udpClientProvider).sendAxis(proto.Axis(
          pitch: gyroPaused
              ? 0
              : (manualJoystick ? _joystick.dy : _attitude.pitch),
          roll:
              gyroPaused ? 0 : (manualJoystick ? _joystick.dx : _attitude.roll),
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

  // SimConnect only has step events (no set-to-detent), so N detents are N
  // sequential press/release pairs.
  Future<void> _stepButton(int incrId, int decrId, int delta) async {
    for (var i = 0; i < delta.abs(); i++) {
      await _tapButton(delta > 0 ? incrId : decrId);
    }
  }

  // HapticFeedback can't loop; this is why the vibration package is needed.
  void _updateStallShaker(bool active) {
    if (active == _stallShaking) return;
    _stallShaking = active;
    if (active) {
      Vibration.vibrate(pattern: const [0, 130, 90], repeat: 0);
    } else {
      Vibration.cancel();
    }
  }

  // Synthesized: MSFS exposes no runway-joint/light signal to read.
  static const _taxiRumbleBandPatterns = [
    [0, 320, 380], // 3-15kt
    [0, 200, 230], // 15-40kt
    [0, 130, 130], // >40kt
  ];

  int _taxiRumbleBandFor(double knots) {
    if (knots < 3) return -1;
    if (knots < 15) return 0;
    if (knots < 40) return 1;
    return 2;
  }

  void _updateTaxiRumble(
      {required bool onGround, required double groundSpeedKnots}) {
    final band = onGround ? _taxiRumbleBandFor(groundSpeedKnots) : -1;
    if (band == _taxiRumbleBand) return;
    _taxiRumbleBand = band;
    if (band < 0) {
      Vibration.cancel();
    } else {
      Vibration.vibrate(pattern: _taxiRumbleBandPatterns[band], repeat: 0);
    }
  }

  @override
  void dispose() {
    _sendTimer?.cancel();
    _attitudeSub?.cancel();
    _attitudeStream.dispose();
    WakelockPlus.disable().catchError((_) {});
    Vibration.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final connectionStatus = ref.watch(connectionStatusProvider).valueOrNull;
    final telemetry = ref.watch(telemetryProvider).valueOrNull;
    final manualJoystick = ref.watch(manualJoystickProvider);
    final nudgeThrottle = ref.watch(nudgeThrottleProvider);
    final layoutEditMode = ref.watch(layoutEditModeProvider);
    final gyroPaused = ref.watch(gyroPausedProvider);

    // Recenter on unpause: the grip the phone settled into while set down
    // is the new neutral, so resuming doesn't jolt the aircraft.
    ref.listen(telemetryProvider, (previous, next) {
      final prevT = previous?.valueOrNull;
      final nextT = next.valueOrNull;

      final was = prevT?.paused ?? false;
      final isNow = nextT?.paused ?? false;
      if (was && !isNow) _attitudeStream.recenter();

      // prevT != null: don't buzz on the first packet after connect.
      if (prevT != null && nextT != null) {
        if (prevT.paused != nextT.paused) HapticFeedback.lightImpact();

        if (prevT.gearPosition != nextT.gearPosition) {
          if (nextT.gearPosition == proto.GearPosition.down) {
            HapticFeedback.heavyImpact();
          } else if (nextT.gearPosition == proto.GearPosition.up) {
            HapticFeedback.mediumImpact();
          }
        }

        if (prevT.flapHandleIndex != nextT.flapHandleIndex) {
          HapticFeedback.lightImpact();
        }

        if (prevT.parkingBrakeSet != nextT.parkingBrakeSet) {
          HapticFeedback.selectionClick();
        }
      }

      // A paused sim keeps reporting the last stallWarning/onGround values.
      final paused = nextT?.paused ?? false;
      _updateStallShaker(!paused && (nextT?.stallWarning ?? false));
      _updateTaxiRumble(
        onGround: !paused && (nextT?.onGround ?? false),
        groundSpeedKnots: nextT?.groundSpeedKnots ?? 0,
      );
    });
    final isPaused = telemetry?.paused ?? false;

    return Scaffold(
      appBar: AppBar(
        centerTitle: false,
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
        // flexibleSpace centers pause across the whole bar, not the gap
        // between title and actions.
        flexibleSpace: SafeArea(
          child: Center(
            child: IconButton(
              icon: Icon(isPaused ? Icons.play_arrow : Icons.pause),
              tooltip: 'Pause',
              color: isPaused ? Theme.of(context).colorScheme.primary : null,
              onPressed: () => _tapButton(ButtonIds.pause),
            ),
          ),
        ),
      ),
      body: SafeArea(
        child: Stack(
          children: [
            Column(
              children: [
                Expanded(
                  child: Row(
                    children: [
                      Expanded(
                        child: Align(
                          // Center drifted down into the rudder control.
                          alignment: Alignment.topCenter,
                          child: _Movable(
                            id: 'attitude',
                            child: manualJoystick
                                ? _ManualJoystick(
                                    onChanged: (v) =>
                                        setState(() => _joystick = v))
                                : _TiltIndicator(
                                    attitude: _attitude,
                                    paused: gyroPaused,
                                    onPausedChanged: (v) => ref
                                        .read(gyroPausedProvider.notifier)
                                        .state = v,
                                  ),
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
                                  onTap: () => _tapButton(ButtonIds.spoilerArm),
                                  active: telemetry?.spoilersArmed ?? false,
                                  compact: true,
                                ),
                                const SizedBox(height: 8),
                                _SpeedbrakeLever(
                                  onStep: (n) => _stepButton(
                                      ButtonIds.spoilerIncr,
                                      ButtonIds.spoilerDecr,
                                      n),
                                  synced: telemetry?.spoilersHandle,
                                ),
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
                            onStep: (n) => _stepButton(
                                ButtonIds.flapIncr, ButtonIds.flapDecr, n),
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
                              label: 'BRK',
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
                      Padding(
                        padding: const EdgeInsets.only(right: 8),
                        child: _Movable(
                          id: 'autopilotDisconnect',
                          child: _ControlLabel(
                            label: 'AP DISC',
                            child: _AutopilotDisconnectButton(
                              engaged: telemetry?.autopilotMaster ?? false,
                              onTap: () =>
                                  _tapButton(ButtonIds.autopilotDisconnect),
                            ),
                          ),
                        ),
                      ),
                      Padding(
                        padding: const EdgeInsets.only(right: 8),
                        child: _Movable(
                          id: 'trim',
                          child: _ControlLabel(
                            label: 'TRIM',
                            child: _TrimRocker(
                              trim: telemetry?.elevatorTrim,
                              onUp: () => _tapButton(ButtonIds.trimUp),
                              onDown: () => _tapButton(ButtonIds.trimDown),
                            ),
                          ),
                        ),
                      ),
                      _Movable(
                        id: 'throttle',
                        child: _ControlLabel(
                          label: 'THROTTLE',
                          child: nudgeThrottle
                              ? _ThrottleNudgeControl(
                                  throttlePercent: telemetry?.throttlePercent,
                                  autothrottle:
                                      telemetry?.autothrottleActive ?? false,
                                  onIncr: () =>
                                      _tapButton(ButtonIds.throttleIncr),
                                  onDecr: () =>
                                      _tapButton(ButtonIds.throttleDecr),
                                  onReverseArmChanged: (pressed) => _sendButton(
                                      ButtonIds.throttleReverseArm, pressed),
                                )
                              : _ThrottleLever(
                                  onChanged: (v) => ref
                                      .read(sliderAxisProvider.notifier)
                                      .setThrottle(v),
                                  synced: telemetry?.throttlePercent,
                                  autothrottle:
                                      telemetry?.autothrottleActive ?? false,
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
            // Bottom: the controls row has no clearance at the top.
            if (layoutEditMode)
              const Positioned(
                bottom: 8,
                left: 0,
                right: 0,
                child: IgnorePointer(
                  child: Center(
                    child: Text(
                      'Drag a control anywhere to reposition it · tap the layout icon to lock',
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

/// Wraps a control so it can be dragged while [layoutEditModeProvider] is on.
///
/// Any nonzero saved offset (or edit mode) moves the real control into the
/// [Overlay], positioned by a [CompositedTransformFollower], while an
/// invisible copy stays inline to reserve the layout slot. A plain
/// `Transform.translate` only moves paint, not the layout slot, and Flex
/// hit-tests by the untouched slot: a moved control overlapping an unmoved
/// sibling's original footprint loses its taps to that sibling. This must hold
/// for locked layouts too, not just mid-drag (it regressed once).
///
/// The whole control is the drag surface. A corner handle was too easy to
/// miss, and the child stays inert ([IgnorePointer]) while dragging.
class _Movable extends ConsumerStatefulWidget {
  const _Movable({required this.id, required this.child});

  final String id;
  final Widget child;

  @override
  ConsumerState<_Movable> createState() => _MovableState();
}

class _MovableState extends ConsumerState<_Movable> {
  final _link = LayerLink();
  final _overlay = OverlayPortalController();

  void _syncOverlay(bool editMode, Offset offset) {
    if (editMode || offset != Offset.zero) {
      _overlay.show();
    } else {
      _overlay.hide();
    }
  }

  @override
  Widget build(BuildContext context) {
    final editMode = ref.watch(layoutEditModeProvider);
    final offset =
        ref.watch(layoutOffsetsProvider.select((m) => m[widget.id])) ??
            Offset.zero;
    final needsOverlay = editMode || offset != Offset.zero;

    ref.listen<bool>(layoutEditModeProvider, (previous, next) {
      _syncOverlay(next, offset);
    });
    ref.listen<Offset?>(
      layoutOffsetsProvider.select((m) => m[widget.id]),
      (previous, next) => _syncOverlay(editMode, next ?? Offset.zero),
    );

    return CompositedTransformTarget(
      link: _link,
      child: OverlayPortal(
        controller: _overlay,
        overlayChildBuilder: (context) => CompositedTransformFollower(
          link: _link,
          showWhenUnlinked: false,
          offset: offset,
          // Overlay entries get tight screen-size constraints; Align loosens
          // them so the control keeps its natural size.
          child: Align(
            alignment: Alignment.topLeft,
            child: KeyedSubtree(
              key: Key(widget.id),
              child: editMode
                  ? _DraggableControl(
                      onPanUpdate: (delta) => ref
                          .read(layoutOffsetsProvider.notifier)
                          .addDelta(widget.id, delta),
                      child: widget.child,
                    )
                  : widget.child,
            ),
          ),
        ),
        // The inline copy is built twice (stateful children tick twice).
        child: needsOverlay
            ? Opacity(opacity: 0, child: IgnorePointer(child: widget.child))
            : KeyedSubtree(key: Key(widget.id), child: widget.child),
      ),
    );
  }
}

/// Edit-mode drag surface: border, plus a scale/tint pulse while grabbed.
class _DraggableControl extends StatefulWidget {
  const _DraggableControl({required this.onPanUpdate, required this.child});

  final ValueChanged<Offset> onPanUpdate;
  final Widget child;

  @override
  State<_DraggableControl> createState() => _DraggableControlState();
}

class _DraggableControlState extends State<_DraggableControl> {
  bool _grabbed = false;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final color = _grabbed ? scheme.secondary : scheme.primary;
    return GestureDetector(
      // The child is IgnorePointer, so deferToChild would never hit.
      behavior: HitTestBehavior.opaque,
      onPanStart: (_) => setState(() => _grabbed = true),
      onPanUpdate: (details) => widget.onPanUpdate(details.delta),
      onPanEnd: (_) => setState(() => _grabbed = false),
      onPanCancel: () => setState(() => _grabbed = false),
      child: AnimatedScale(
        scale: _grabbed ? 1.05 : 1.0,
        duration: const Duration(milliseconds: 120),
        curve: Curves.easeOut,
        child: DecoratedBox(
          decoration: BoxDecoration(
            color: _grabbed ? color.withValues(alpha: 0.15) : null,
            border: Border.all(color: color, width: 2),
            borderRadius: BorderRadius.circular(8),
            boxShadow: _grabbed
                ? [
                    BoxShadow(
                      color: Colors.black.withValues(alpha: 0.3),
                      blurRadius: 10,
                      offset: const Offset(0, 3),
                    ),
                  ]
                : null,
          ),
          child: IgnorePointer(child: widget.child),
        ),
      ),
    );
  }
}

/// Small caption above a control.
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

class _ConnectionStatusText extends StatelessWidget {
  const _ConnectionStatusText({required this.status});

  final ConnectionStatus? status;

  @override
  Widget build(BuildContext context) {
    // versionMismatch, error and null all read as disconnected.
    final (label, color) = switch (status) {
      ConnectionStatus.connected => ('Connected', Colors.green),
      ConnectionStatus.connecting => ('Connecting', Colors.orange),
      _ => ('Disconnected', Colors.red),
    };
    return BrandTitle(
      trailing: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
        decoration: BoxDecoration(
          color: color.withValues(alpha: 0.15),
          borderRadius: BorderRadius.circular(12),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.circle, size: 8, color: color),
            const SizedBox(width: 5),
            Text(
              label,
              style: TextStyle(
                  color: color, fontWeight: FontWeight.w600, fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}

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

/// Always red; dimmed while the autopilot is already off ([engaged]).
class _AutopilotDisconnectButton extends StatelessWidget {
  const _AutopilotDisconnectButton(
      {required this.onTap, required this.engaged});

  final VoidCallback onTap;
  final bool engaged;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return SizedBox(
      width: 72,
      height: 72,
      child: FilledButton(
        onPressed: onTap,
        style: FilledButton.styleFrom(
          backgroundColor:
              engaged ? scheme.error : scheme.error.withValues(alpha: 0.35),
          foregroundColor: scheme.onError,
          shape: const CircleBorder(),
          padding: EdgeInsets.zero,
        ),
        child: const Text('AP', style: TextStyle(fontWeight: FontWeight.bold)),
      ),
    );
  }
}

/// Artificial horizon with a pause button for [gyroPausedProvider]: paused
/// withholds only pitch/roll, and the display stays live (dimmed).
class _TiltIndicator extends StatelessWidget {
  const _TiltIndicator({
    required this.attitude,
    required this.paused,
    required this.onPausedChanged,
  });

  final Attitude attitude;
  final bool paused;
  final ValueChanged<bool> onPausedChanged;

  // Must match AttitudeStream's default maxTiltRadians.
  static const _maxTiltDegrees = 45.0;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final pitchDeg = attitude.pitch * _maxTiltDegrees;
    final rollDeg = attitude.roll * _maxTiltDegrees;
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(8),
          child: SizedBox(
            width: 160,
            height: 160,
            child: Stack(
              children: [
                Container(
                  decoration: BoxDecoration(
                    border: Border.all(color: Colors.grey),
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: Opacity(
                    opacity: paused ? 0.4 : 1.0,
                    child: CustomPaint(
                      size: const Size(160, 160),
                      painter:
                          _HorizonPainter(pitchDeg: pitchDeg, rollDeg: rollDeg),
                    ),
                  ),
                ),
                Positioned(
                  top: 2,
                  right: 2,
                  child: IconButton(
                    icon:
                        Icon(paused ? Icons.play_arrow : Icons.pause, size: 18),
                    tooltip: paused ? 'Resume gyroscope' : 'Pause gyroscope',
                    color: paused ? scheme.primary : Colors.white,
                    constraints:
                        const BoxConstraints(minWidth: 28, minHeight: 28),
                    padding: EdgeInsets.zero,
                    onPressed: () => onPausedChanged(!paused),
                  ),
                ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 4),
        Text('P: ${pitchDeg.round()}°  R: ${rollDeg.round()}°'
            '${paused ? " (paused)" : ""}'),
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
    final pitchOffset =
        pitchDeg / _TiltIndicator._maxTiltDegrees * size.height / 2;
    // Oversized so rotated rectangles still cover the corners.
    final oversize = size.longestSide * 2;

    canvas.save();
    canvas.translate(center.dx, center.dy);
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

/// Throttle lever: -1 (full reverse) .. 1. Forward holds where dropped; the
/// reverse half snaps back to idle on release so reverse can't be left on.
///
/// [synced] is the sim's throttle (TELEMETRY); the lever mirrors it unless
/// the pilot is dragging or the settle rule below applies.
class _ThrottleLever extends StatefulWidget {
  const _ThrottleLever(
      {required this.onChanged, this.synced, this.autothrottle = false});

  final ValueChanged<double> onChanged;
  final double? synced;

  final bool autothrottle;

  @override
  State<_ThrottleLever> createState() => _ThrottleLeverState();
}

class _ThrottleLeverState extends State<_ThrottleLever>
    with SingleTickerProviderStateMixin {
  // 320 overflows the row by 8px.
  static const _height = 300.0;
  static const _knobHeight = 36.0;

  static const _forwardFraction = 0.8;

  // Alignment y (-1 top .. 1 bottom) of idle and of a throttle value.
  static const _idleY = -1 + 2 * _forwardFraction;
  double _knobY(double v) =>
      v >= 0 ? _idleY + (-1 - _idleY) * v : _idleY - (1 - _idleY) * v;

  double _value = 0; // -1..1
  double _animFrom = 0;
  double _animTo = 0;
  // True for the idle spring-back; false when only following a synced value
  // (must not re-send throttle values the sim already reports).
  bool _animNotifies = true;
  bool _dragging = false;
  late final AnimationController _anim;

  // After a local command the phone owns the throttle: `synced` is ignored
  // until the sim's lever stops moving. Many aircraft slew the lever, so
  // `synced` is mid-travel for a while and following it would drag the knob
  // backwards (e.g. a reverse-zone release back into reverse).
  bool _awaitingSim = false;
  DateTime _lastLocalCommandAt = DateTime.fromMillisecondsSinceEpoch(0);
  double? _lastSynced;
  DateTime _syncedMovedAt = DateTime.fromMillisecondsSinceEpoch(0);
  static const _simSettleTime = Duration(milliseconds: 500);

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
    _awaitingSim = true;
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
    final now = DateTime.now();
    if (synced != null &&
        (_lastSynced == null || (synced - _lastSynced!).abs() > 0.005)) {
      _syncedMovedAt = now;
    }
    _lastSynced = synced;
    if (_awaitingSim) {
      final quietSince = _syncedMovedAt.isAfter(_lastLocalCommandAt)
          ? _syncedMovedAt
          : _lastLocalCommandAt;
      if (now.difference(quietSince) < _simSettleTime) return;
      _awaitingSim = false;
    }
    // Compare with _animTo, not _value, so rebuilds with the same reading
    // don't restart the animation.
    if (!_dragging && synced != null && synced != _animTo) {
      _animateTo(synced,
          notify: false, duration: const Duration(milliseconds: 100));
    }
  }

  void _onDragStart(DragStartDetails details) {
    _dragging = true;
    _anim.stop();
  }

  void _onDragUpdate(DragUpdateDetails details) {
    setState(() {
      // Per-zone scale keeps the knob 1:1 under the finger.
      final zoneFraction =
          _value >= 0 ? _forwardFraction : (1 - _forwardFraction);
      _value = (_value - details.delta.dy / (_height * zoneFraction))
          .clamp(-1.0, 1.0);
    });
    _notifyChanged();
  }

  // A release below this snaps to exact idle.
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

  static const _scaleMarks = <(int, double)>[
    (100, 1.0),
    (75, 0.75),
    (50, 0.5),
    (25, 0.25),
    (-100, -1.0),
  ];

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return GestureDetector(
      onVerticalDragStart: _onDragStart,
      onVerticalDragUpdate: _onDragUpdate,
      onVerticalDragEnd: _onDragEnd,
      child: SizedBox(
        height: _height,
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            SizedBox(
              width: 28,
              height: _height,
              child: Stack(
                children: [
                  for (final (label, v) in _scaleMarks)
                    Align(
                      alignment: Alignment(0, _knobY(v)),
                      child:
                          Text('$label', style: const TextStyle(fontSize: 9)),
                    ),
                ],
              ),
            ),
            const SizedBox(width: 4),
            Container(
              width: 64,
              height: _height,
              decoration: BoxDecoration(
                color: scheme.surfaceContainerHighest,
                borderRadius: BorderRadius.circular(12),
              ),
              child: Stack(
                alignment: Alignment.center,
                children: [
                  Positioned(
                    top: 6,
                    child: widget.autothrottle
                        ? Container(
                            padding: const EdgeInsets.symmetric(
                                horizontal: 4, vertical: 1),
                            decoration: BoxDecoration(
                              color: scheme.tertiary,
                              borderRadius: BorderRadius.circular(4),
                            ),
                            child: Text('A/T',
                                style: TextStyle(
                                    fontSize: 11,
                                    fontWeight: FontWeight.bold,
                                    color: scheme.onTertiary)),
                          )
                        : const Text('THR', style: TextStyle(fontSize: 11)),
                  ),
                  Align(
                    alignment: const Alignment(0, _idleY),
                    child: Container(
                        width: double.infinity,
                        height: 1,
                        color: scheme.outline),
                  ),
                  Align(
                    alignment: Alignment(0, _knobY(_value)),
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
          ],
        ),
      ),
    );
  }
}

/// Flap lever with discrete detents 0..[max]. Releasing on a new detent sends
/// one INCR/DECR tap per detent crossed.
///
/// [synced] is the sim's flap handle index, mirrored while not dragging.
class _FlapLever extends StatefulWidget {
  const _FlapLever(
      {required this.onStep, required this.synced, required this.max});

  /// Positive = FLAPS_INCR taps, negative = FLAPS_DECR taps.
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

  // Right after a tap `synced` is still the pre-tap value for a round trip;
  // without this the lever snaps back, then forward again.
  DateTime _lastLocalCommandAt = DateTime.fromMillisecondsSinceEpoch(0);
  static const _localCommandGrace = Duration(milliseconds: 500);

  int get _max => widget.max ?? 0;

  double _fractionFor(int index) => _max == 0 ? 0 : index / _max;

  // Use this, not widget.synced, for drag math: a second drag inside the
  // grace window would otherwise diff against the stale value and double-send.
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

/// Spring-loaded rudder: snaps back to neutral on release.
class _RudderStick extends StatefulWidget {
  const _RudderStick({required this.onChanged});

  final ValueChanged<double> onChanged;

  @override
  State<_RudderStick> createState() => _RudderStickState();
}

class _RudderStickState extends State<_RudderStick>
    with SingleTickerProviderStateMixin {
  static const _width = 240.0;
  static const _knobWidth = 44.0;

  // Readout scale only; the real deflection is the aircraft's.
  static const _maxDeflectionDeg = 30;

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
    final deg = (_value.abs() * _maxDeflectionDeg).round();
    final deflectionLabel =
        deg == 0 ? '0°' : (_value < 0 ? 'L $deg°' : 'R $deg°');
    return GestureDetector(
      onHorizontalDragUpdate: _onDragUpdate,
      onHorizontalDragEnd: _onDragEnd,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
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
          const SizedBox(height: 2),
          Text(deflectionLabel, style: const TextStyle(fontSize: 11)),
        ],
      ),
    );
  }
}

/// 2D spring-back pitch/roll input, replacing tilt when enabled in settings.
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

/// [active] reflects the sim's state, not just the last tap.
class _BigButton extends StatelessWidget {
  const _BigButton(
      {required this.label,
      required this.onTap,
      this.active = false,
      this.compact = false});

  final String label;
  final VoidCallback onTap;
  final bool active;
  final bool compact;

  // Default M3 padding exceeds the 64px compact width and wraps "ARM".
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

/// Speedbrake lever with detents 0..2. Crossing a detent sends one
/// SPOILERS_INC/DEC tap.
///
/// [synced] is SPOILERS HANDLE POSITION (0..1); the knob sits at the nearest
/// detent while not dragging.
class _SpeedbrakeLever extends StatefulWidget {
  const _SpeedbrakeLever({required this.onStep, this.synced});

  /// Positive = SPOILERS_INC taps, negative = SPOILERS_DEC taps.
  final ValueChanged<int> onStep;
  final double? synced;

  @override
  State<_SpeedbrakeLever> createState() => _SpeedbrakeLeverState();
}

class _SpeedbrakeLeverState extends State<_SpeedbrakeLever> {
  static const _detentMax = 2;
  static const _height = 160.0;
  static const _knobHeight = 32.0;

  double _dragValue = 0; // fractional position while dragging, 0.._detentMax
  int _detent = 0;
  bool _dragging = false;

  @override
  void didUpdateWidget(covariant _SpeedbrakeLever oldWidget) {
    super.didUpdateWidget(oldWidget);
    final synced = widget.synced;
    if (_dragging || synced == null) return;
    final detent = (synced * _detentMax).round().clamp(0, _detentMax);
    if (detent != _detent) {
      _detent = detent;
      _dragValue = detent.toDouble();
    }
  }

  void _onDragStart(DragStartDetails details) {
    _dragging = true;
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
    _dragging = false;
    setState(() => _dragValue = _detent.toDouble());
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

/// Tap-and-hold full brake. The wire protocol has no proportional brake
/// (the brake axes are non-linear). With the parking brake engaged this may
/// show no effect.
class _ToeBrake extends StatefulWidget {
  const _ToeBrake({required this.label, required this.onPressedChanged});

  final String label;
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
    // No onTapCancel when navigating away mid-hold; release or the brake
    // stays applied.
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
        child: Text(widget.label,
            style:
                TextStyle(color: _pressed ? scheme.onError : scheme.onSurface)),
      ),
    );
  }
}

/// Fires [onStep] on press, then every 150ms while held.
class _RepeatButton extends StatefulWidget {
  const _RepeatButton({required this.label, required this.onStep});

  final String label;
  final VoidCallback onStep;

  @override
  State<_RepeatButton> createState() => _RepeatButtonState();
}

class _RepeatButtonState extends State<_RepeatButton> {
  Timer? _timer;

  void _start() {
    widget.onStep();
    _timer = Timer.periodic(
        const Duration(milliseconds: 150), (_) => widget.onStep());
  }

  void _stop() {
    _timer?.cancel();
    _timer = null;
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return GestureDetector(
      onTapDown: (_) => _start(),
      onTapUp: (_) => _stop(),
      onTapCancel: _stop,
      child: Container(
        width: 72,
        height: 56,
        decoration: BoxDecoration(
          color: scheme.surfaceContainerHighest,
          borderRadius: BorderRadius.circular(8),
        ),
        alignment: Alignment.center,
        child: Text(widget.label,
            style: TextStyle(fontSize: 24, color: scheme.onSurface)),
      ),
    );
  }
}

/// Pitch trim: hold to repeat ELEV_TRIM_UP/DN steps, which the sim itself
/// accelerates while held.
class _TrimRocker extends StatelessWidget {
  const _TrimRocker({required this.onUp, required this.onDown, this.trim});

  final VoidCallback onUp;
  final VoidCallback onDown;

  /// ELEVATOR TRIM PCT (-1..1) from TELEMETRY.
  final double? trim;

  @override
  Widget build(BuildContext context) {
    final t = trim;
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        _RepeatButton(label: '▲', onStep: onUp),
        const SizedBox(height: 4),
        Text(t == null ? '--' : '${(t * 100).round()}%',
            style: const TextStyle(fontSize: 11)),
        const SizedBox(height: 4),
        _RepeatButton(label: '▼', onStep: onDown),
      ],
    );
  }
}

/// Relative throttle for when the drag lever is too imprecise: +/- fire
/// INCREASE/DECREASE_THROTTLE and holding REV only arms reverse thrust, so no
/// absolute axis value is sent and it works on any aircraft. The readout is
/// the sim's own position ([throttlePercent]).
class _ThrottleNudgeControl extends StatelessWidget {
  const _ThrottleNudgeControl({
    required this.throttlePercent,
    this.autothrottle = false,
    required this.onIncr,
    required this.onDecr,
    required this.onReverseArmChanged,
  });

  final double? throttlePercent;
  final bool autothrottle;
  final VoidCallback onIncr;
  final VoidCallback onDecr;
  final ValueChanged<bool> onReverseArmChanged;

  @override
  Widget build(BuildContext context) {
    final pct = throttlePercent ?? 0;
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text((autothrottle ? 'A/T ' : '') +
            (pct < 0 ? 'REV' : '${(pct.clamp(0.0, 1.0) * 100).round()}%')),
        const SizedBox(height: 8),
        _RepeatButton(label: '+', onStep: onIncr),
        const SizedBox(height: 8),
        _RepeatButton(label: '-', onStep: onDecr),
        const SizedBox(height: 16),
        _ToeBrake(label: 'REV', onPressedChanged: onReverseArmChanged),
      ],
    );
  }
}
