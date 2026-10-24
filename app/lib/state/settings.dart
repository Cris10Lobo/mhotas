import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _prefsManualJoystickKey = 'mhotas.manualJoystick.enabled';

/// Whether the on-screen joystick drives pitch/roll instead of the tilt sensor.
class ManualJoystickNotifier extends StateNotifier<bool> {
  ManualJoystickNotifier() : super(false) {
    _load();
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    state = prefs.getBool(_prefsManualJoystickKey) ?? false;
  }

  Future<void> setEnabled(bool value) async {
    state = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_prefsManualJoystickKey, value);
  }
}

final manualJoystickProvider =
    StateNotifierProvider<ManualJoystickNotifier, bool>(
  (ref) => ManualJoystickNotifier(),
);

const _prefsNudgeThrottleKey = 'mhotas.nudgeThrottle.enabled';

/// Whether throttle is +/- nudge buttons (plus hold-for-reverse) instead of the lever.
class NudgeThrottleNotifier extends StateNotifier<bool> {
  NudgeThrottleNotifier() : super(false) {
    _load();
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    state = prefs.getBool(_prefsNudgeThrottleKey) ?? false;
  }

  Future<void> setEnabled(bool value) async {
    state = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_prefsNudgeThrottleKey, value);
  }
}

final nudgeThrottleProvider =
    StateNotifierProvider<NudgeThrottleNotifier, bool>(
  (ref) => NudgeThrottleNotifier(),
);
