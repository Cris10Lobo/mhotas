import 'dart:async';
import 'dart:math' as math;

import 'package:flutter_rotation_sensor/flutter_rotation_sensor.dart';

/// Pitch/roll in -1..1. Only the recenter offset is applied here (it corrects
/// the current grip); dead-zone and curve are server-side.
class Attitude {
  final double pitch;
  final double roll;
  const Attitude({required this.pitch, required this.roll});
}

/// Wraps flutter_rotation_sensor's OS-fused orientation stream.
class AttitudeStream {
  AttitudeStream({double maxTiltRadians = math.pi / 4})
      : _maxTiltRadians = maxTiltRadians;

  final double _maxTiltRadians;

  double _pitchOffset = 0;
  double _rollOffset = 0;
  double _lastRawPitch = 0;
  double _lastRawRoll = 0;

  StreamSubscription? _subscription;
  final _controller = StreamController<Attitude>.broadcast();

  Stream<Attitude> get stream => _controller.stream;

  void start() {
    RotationSensor.samplingPeriod = SensorInterval.gameInterval;
    _subscription = RotationSensor.orientationStream.listen((event) {
      // The plugin's `pitch` is asin-derived and capped at ±90°, and a normal
      // grip already sits at ~50-55° there, so full yoke deflection was
      // unreachable. Remapping puts bank in the capped slot and reads pitch
      // from the uncapped atan2-derived `roll`.
      final remapped = event.remapCoordinateSystem(-Axis3.Y, Axis3.X);
      _lastRawPitch = remapped.eulerAngles.roll;
      _lastRawRoll = remapped.eulerAngles.pitch;
      _emit();
    });
  }

  /// Takes the current raw orientation as zero.
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
