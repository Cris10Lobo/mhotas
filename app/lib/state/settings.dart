import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _prefsManualJoystickKey = 'mhotas.manualJoystick.enabled';

/// Whether the on-screen 2D joystick drives pitch/roll instead of the tilt
/// sensor. Persisted the same way connect_screen.dart persists host/port —
/// SharedPreferences, no backend involved, since this is a phone-only UI
/// choice rather than something the sim needs to know about.
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

/// Whether the throttle control is +/- nudge buttons (plus a hold-for-
/// reverse button) instead of the drag lever — a fallback for phones/
/// aircraft where the lever is hard to use precisely. Same persistence
/// pattern as [ManualJoystickNotifier].
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
