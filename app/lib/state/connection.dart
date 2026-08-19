import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../network/udp_client.dart';

/// Single shared UdpClient for the app's lifetime. Disposed when the
/// provider container is torn down.
final udpClientProvider = Provider<UdpClient>((ref) {
  final client = UdpClient(deviceName: 'mHOTAS phone');
  ref.onDispose(client.dispose);
  return client;
});

final connectionStatusProvider = StreamProvider<ConnectionStatus>((ref) {
  return ref.watch(udpClientProvider).status;
});
