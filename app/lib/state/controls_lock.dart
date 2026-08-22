import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Whether the app is withholding outgoing AXIS/BUTTON traffic so the pilot
/// can set the phone down (to focus on the sim, or just free a hand)
/// without stray tilt/touch input reaching the aircraft — see
/// control_screen.dart's `_ControlsLockSlide`. No protocol change needed:
/// the server's existing ~500ms AXIS-timeout failsafe already neutrals
/// pitch/roll/rudder and holds throttle once packets stop arriving, which
/// is exactly the behavior wanted here.
///
/// Not persisted — always starts unlocked, matching layoutEditModeProvider's
/// same reasoning: a fresh launch should never come up in a state that
/// silently withholds control input with no visible cause.
final controlsLockedProvider = StateProvider<bool>((ref) => false);
