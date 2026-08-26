import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../network/protocol.dart' as proto;
import 'connection.dart';

/// Latest CONFIG packet from the server — its current dead-zone/expo/
/// invert/smoothing for every axis, driving settings_screen.dart's
/// calibration editor. Arrives unprompted right after HELLO_ACK and again
/// after every edit the phone sends.
final calibrationProvider = StreamProvider<proto.Config>((ref) {
  return ref.watch(udpClientProvider).config;
});
