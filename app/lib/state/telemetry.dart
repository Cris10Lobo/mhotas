import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../network/protocol.dart' as proto;
import 'connection.dart';

final telemetryProvider = StreamProvider<proto.Telemetry>((ref) {
  return ref.watch(udpClientProvider).telemetry;
});
