import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../network/protocol.dart' as proto;
import 'connection.dart';

/// Latest TELEMETRY packet from the server (flap/gear/throttle/brake/pause
/// state), driving the live indicators in control_screen.dart.
final telemetryProvider = StreamProvider<proto.Telemetry>((ref) {
  return ref.watch(udpClientProvider).telemetry;
});
