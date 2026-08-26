import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Whether tilt (pitch/roll) input is withheld from outgoing AXIS packets —
/// toggled by the pause icon on the attitude display in control_screen.dart.
/// Lets the pilot set the phone down without stray tilt reaching the
/// aircraft, while throttle/rudder/buttons keep working normally.
///
/// Not persisted — always starts unpaused, so a fresh launch never comes up
/// silently withholding tilt input.
final gyroPausedProvider = StateProvider<bool>((ref) => false);
