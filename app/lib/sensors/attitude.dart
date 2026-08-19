import 'dart:async';
import 'dart:math' as math;

import 'package:flutter_rotation_sensor/flutter_rotation_sensor.dart';

/// Normalized phone attitude: pitch/roll in -1..1, matching the AXIS
/// packet's range. No dead-zone/curve shaping happens here — that's
/// server-side — but the recenter/zero offset does, since it's correcting
/// for however the user is currently gripping the phone, not for how the
/// simulator should respond.
class Attitude {
  final double pitch;
  final double roll;
  const Attitude({required this.pitch, required this.roll});
}

/// Wraps flutter_rotation_sensor's fused orientation stream (OS-level
/// sensor fusion — CoreMotion on iOS, the rotation vector sensor on
/// Android — rather than hand-rolling accelerometer+gyroscope fusion in
/// Dart). Validated live on a real Android device; motion sensors aren't
/// meaningful on emulators/simulators so this can't be exercised there.
class AttitudeStream {
  AttitudeStream({double maxTiltRadians = math.pi / 4}) : _maxTiltRadians = maxTiltRadians;

  /// Radians of physical tilt that map to full ±1 deflection. Default ±45°.
  final double _maxTiltRadians;

  double _pitchOffset = 0;
  double _rollOffset = 0;
  double _lastRawPitch = 0;
  double _lastRawRoll = 0;

  StreamSubscription? _subscription;
  final _controller = StreamController<Attitude>.broadcast();

  Stream<Attitude> get stream => _controller.stream;

  void start() {
    RotationSensor.samplingPeriod = SensorInterval.gameInterval; // ~20ms / 50Hz
    _subscription = RotationSensor.orientationStream.listen((event) {
      // flutter_rotation_sensor's own `pitch` is the asin-derived middle
      // angle of its Euler decomposition, hard-bounded to ±90° — and that
      // singularity lands exactly on the axis this app rotates around for
      // a "yoke" pull (confirmed live: normal grip already sits ~50-55°
      // raw, leaving only ~35-40° of headroom before the ±90° ceiling,
      // which is what made full deflection unreachable and roll snap near
      // it). Remapping the device axes swaps which physical rotation lands
      // in the bounded slot: bank (small-motion in practice, rotating
      // about the phone's long axis) now takes the bounded one, and pitch
      // (the full-range one in practice) reads from the atan2-derived
      // `roll` field instead, which has no ±90° ceiling.
      final remapped = event.remapCoordinateSystem(-Axis3.Y, Axis3.X);
      _lastRawPitch = remapped.eulerAngles.roll;
      _lastRawRoll = remapped.eulerAngles.pitch;
      _emit();
    });
  }

  /// Captures the current raw orientation as the new zero point. Call this
  /// when the user taps "recenter" while gripping the phone the way they
  /// intend to fly with it.
  void recenter() {
    _pitchOffset = _lastRawPitch;
    _rollOffset = _lastRawRoll;
    _emit();
  }

  void _emit() {
    _controller.add(Attitude(
      pitch: _normalize(_lastRawPitch - _pitchOffset),
      roll: _normalize(_lastRawRoll - _rollOffset),
    ));
  }

  double _normalize(double radians) {
    final v = radians / _maxTiltRadians;
    if (v > 1) return 1;
    if (v < -1) return -1;
    return v;
  }

  void dispose() {
    _subscription?.cancel();
    _controller.close();
  }
}
