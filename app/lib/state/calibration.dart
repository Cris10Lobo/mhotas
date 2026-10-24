import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../network/protocol.dart' as proto;
import 'connection.dart';

final calibrationProvider = StreamProvider<proto.Config>((ref) {
  return ref.watch(udpClientProvider).config;
});
