// A manually set throttle must stay where the pilot dropped it, on the phone
// and in the sim. On the FBW A32NX the sim's lever reading isn't the phone's
// value (its quadrant has detents), and the lever was seen creeping away
// from a mid-travel setting after release.
//
//   flutter test integration_test/throttle_hold_test.dart -d <device> \
//     --dart-define=MHOTAS_SERVER=192.168.0.101:9750
//
// Needs an aircraft on the ground, parking brake set, autothrust off; leaves
// the throttle at idle.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:mhotas/network/protocol.dart' as proto;
import 'package:mhotas/network/udp_client.dart';
import 'package:mhotas/state/connection.dart';
import 'package:mhotas/ui/control_screen.dart';

const _server = String.fromEnvironment('MHOTAS_SERVER');

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  late ProviderContainer container;
  proto.Telemetry? sim;

  setUp(() async {
    expect(_server, isNotEmpty, reason: '--dart-define=MHOTAS_SERVER=ip:port');
    container = ProviderContainer();
    final client = container.read(udpClientProvider);
    client.telemetry.listen((t) => sim = t);
    final connected = client.status
        .firstWhere((s) => s == ConnectionStatus.connected)
        .timeout(const Duration(seconds: 5));
    final [host, port] = _server.split(':');
    await client.connect(host, int.parse(port));
    await connected;
  });

  tearDown(() => container.dispose());

  testWidgets('manual throttle holds where it was dropped', (tester) async {
    await SystemChrome.setPreferredOrientations(
        [DeviceOrientation.landscapeLeft]);
    await SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky);
    await tester.runAsync(() => Future.delayed(const Duration(seconds: 2)));
    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: ControlScreen()),
    ));

    Future<void> pumpFor(Duration d) async {
      final end = DateTime.now().add(d);
      while (DateTime.now().isBefore(end)) {
        await tester.pump(const Duration(milliseconds: 50));
      }
    }

    final throttle = find.byKey(const Key('throttle'));
    final track = find.descendant(
        of: throttle,
        matching: find.byWidgetPredicate((w) =>
            w is Container &&
            w.constraints?.maxWidth == 64 &&
            w.constraints?.minWidth == 64));
    final knob = find.descendant(
        of: throttle,
        matching: find.byWidgetPredicate(
            (w) => w is Container && w.constraints?.maxHeight == 36));

    // Inverts _ThrottleLever's _knobY for the forward zone.
    double knobValue() {
      final t = tester.getRect(track.first);
      final k = tester.getRect(knob.first);
      final y =
          (k.center.dy - t.top - k.height / 2) / (t.height - k.height) * 2 - 1;
      const idleY = 0.6;
      return (y - idleY) / (-1 - idleY);
    }

    await pumpFor(const Duration(seconds: 1));
    expect(sim, isNotNull, reason: 'no telemetry');
    expect(sim!.paused, isFalse, reason: 'unpause the sim first');
    expect(sim!.autothrottleActive, isFalse, reason: 'turn autothrust off');

    // Idle first so every run starts from the same place.
    await tester.drag(throttle, const Offset(0, 400));
    await pumpFor(const Duration(seconds: 3));

    // Roughly half the forward travel, between the A320's CL and FLX detents
    // depending on how they're configured.
    await tester.drag(throttle, const Offset(0, -110));
    await tester.pump();
    final dropped = knobValue();
    debugPrint('THR dropped phone=${dropped.toStringAsFixed(3)}');

    // Watch for 12s: the first ~1s is the sim slewing, the rest must hold.
    final trace = <(int, double, double)>[];
    final start = DateTime.now();
    while (DateTime.now().difference(start) < const Duration(seconds: 12)) {
      await tester.pump(const Duration(milliseconds: 250));
      final ms = DateTime.now().difference(start).inMilliseconds;
      trace.add((ms, knobValue(), sim!.throttlePercent));
      debugPrint('THR t=${ms}ms phone=${trace.last.$2.toStringAsFixed(3)} '
          'sim=${trace.last.$3.toStringAsFixed(3)}');
    }

    final settled = trace.where((s) => s.$1 > 3000).toList();
    final simSettled = settled.first.$3;
    final phoneDrift = settled
        .map((s) => (s.$2 - dropped).abs())
        .reduce((a, b) => a > b ? a : b);
    final simDrift = settled
        .map((s) => (s.$3 - simSettled).abs())
        .reduce((a, b) => a > b ? a : b);
    debugPrint('THR summary dropped=${dropped.toStringAsFixed(3)} '
        'simSettled=${simSettled.toStringAsFixed(3)} '
        'phoneDrift=${phoneDrift.toStringAsFixed(3)} '
        'simDrift=${simDrift.toStringAsFixed(3)}');

    await tester.drag(throttle, const Offset(0, 400));
    await pumpFor(const Duration(seconds: 2));
    await tester.pumpWidget(const SizedBox());
    await SystemChrome.setPreferredOrientations([]);
    await SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);

    expect(simDrift, lessThan(0.02), reason: 'sim lever moved after settling');
    expect(phoneDrift, lessThan(0.03),
        reason: 'phone lever was dragged away from where it was dropped');
  });
}
