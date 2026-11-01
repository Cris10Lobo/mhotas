import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Tilt (pitch/roll) bypasses Riverpod: it updates up to 50Hz, too often for widget state.
class SliderAxisState {
  // NaN = not moved since connecting: the server leaves the sim's lever alone.
  final double throttle; // -1..1, negative = reverse thrust
  final double rudder; // -1..1
  final double speedbrake; // 0..1

  const SliderAxisState(
      {this.throttle = double.nan,
      this.rudder = 0,
      this.speedbrake = double.nan});

  SliderAxisState copyWith(
      {double? throttle, double? rudder, double? speedbrake}) {
    return SliderAxisState(
      throttle: throttle ?? this.throttle,
      rudder: rudder ?? this.rudder,
      speedbrake: speedbrake ?? this.speedbrake,
    );
  }
}

class SliderAxisNotifier extends StateNotifier<SliderAxisState> {
  SliderAxisNotifier() : super(const SliderAxisState());

  void setThrottle(double value) => state = state.copyWith(throttle: value);
  void setRudder(double value) => state = state.copyWith(rudder: value);
  void setSpeedbrake(double value) => state = state.copyWith(speedbrake: value);

  /// On every (re)connect, so a stale lever value can't override the sim.
  void reset() => state = const SliderAxisState();
}

final sliderAxisProvider =
    StateNotifierProvider<SliderAxisNotifier, SliderAxisState>(
  (ref) => SliderAxisNotifier(),
);
