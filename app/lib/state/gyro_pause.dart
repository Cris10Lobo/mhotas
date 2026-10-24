import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Not persisted, so a fresh launch never silently withholds tilt input.
final gyroPausedProvider = StateProvider<bool>((ref) => false);
