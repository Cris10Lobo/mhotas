import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Tilt (pitch/roll) bypasses Riverpod: it updates up to 50Hz, too often for widget state.
class SliderAxisState {
  final double throttle; // -1..1, negative = reverse thrust
  final double rudder; // -1..1
  final double speedbrake; // 0..1

  const SliderAxisState(
      {this.throttle = 0, this.rudder = 0, this.speedbrake = 0});

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
}

final sliderAxisProvider =
    StateNotifierProvider<SliderAxisNotifier, SliderAxisState>(
  (ref) => SliderAxisNotifier(),
);
