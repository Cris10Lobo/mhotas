import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Throttle/rudder values driven by the on-screen sliders in
/// control_screen.dart. Tilt (pitch/roll) comes from AttitudeStream
/// directly rather than through Riverpod state, since it updates far more
/// often (up to 50Hz) than widget-tree state changes should.
class SliderAxisState {
  final double throttle; // -1..1, negative = reverse thrust
  final double rudder; // -1..1

  const SliderAxisState({this.throttle = 0, this.rudder = 0});

  SliderAxisState copyWith({double? throttle, double? rudder}) {
    return SliderAxisState(
      throttle: throttle ?? this.throttle,
      rudder: rudder ?? this.rudder,
    );
  }
}

class SliderAxisNotifier extends StateNotifier<SliderAxisState> {
  SliderAxisNotifier() : super(const SliderAxisState());

  void setThrottle(double value) => state = state.copyWith(throttle: value);
  void setRudder(double value) => state = state.copyWith(rudder: value);
}

final sliderAxisProvider = StateNotifierProvider<SliderAxisNotifier, SliderAxisState>(
  (ref) => SliderAxisNotifier(),
);
