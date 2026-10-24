import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../network/udp_client.dart';

final udpClientProvider = Provider<UdpClient>((ref) {
  final client = UdpClient(deviceName: 'mHOTAS phone');
  ref.onDispose(client.dispose);
  return client;
});

final connectionStatusProvider = StreamProvider<ConnectionStatus>((ref) {
  return ref.watch(udpClientProvider).status;
});
