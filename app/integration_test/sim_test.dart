// Drives the real flying screen on a phone against a real mhotas-server and
// MSFS, and checks the sim's side through TELEMETRY:
//
//   flutter test integration_test -d <device> \
//     --dart-define=MHOTAS_SERVER=192.168.0.101:9750
//     [--dart-define=MHOTAS_NO_SPOILERS=true]
//
// Needs an aircraft loaded on the ground and unpaused; moves the speedbrake,
// flaps and parking brake, and puts flaps and parking brake back.

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
// For aircraft without spoilers (Cessna 172): skips ARM and speedbrake.
const _noSpoilers = bool.fromEnvironment('MHOTAS_NO_SPOILERS');

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  late ProviderContainer container;
  proto.Telemetry? sim;

  // Pumps frames until the sim reports [ok], so the UI keeps up meanwhile.
  Future<proto.Telemetry> waitSim(WidgetTester tester, String what,
      bool Function(proto.Telemetry) ok) async {
    final deadline = DateTime.now().add(const Duration(seconds: 5));
    // At least one frame, so widgets reflect [sim] before the caller acts.
    await tester.pump();
    while (sim == null || !ok(sim!)) {
      if (DateTime.now().isAfter(deadline)) {
        fail('sim never reported $what; last telemetry: ${_describe(sim)}');
      }
      await tester.pump(const Duration(milliseconds: 50));
    }
    debugPrint('SIM OK $what: ${_describe(sim)}');
    return sim!;
  }

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

  testWidgets('phone controls reach the sim', (tester) async {
    // As main() does; in portrait half the controls lay out off-screen.
    await SystemChrome.setPreferredOrientations(
        [DeviceOrientation.landscapeLeft]);
    await SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky);
    await tester.runAsync(() => Future.delayed(const Duration(seconds: 2)));
    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: ControlScreen()),
    ));
    final start = await waitSim(tester, 'first telemetry', (_) => true);
    expect(start.paused, isFalse, reason: 'unpause the sim first');

    if (!_noSpoilers) {
      // Spoiler ARM first: on the A32NX arming retracts a deployed handle while
      // the vJoy axis stays put, so the speedbrake step below re-sends the
      // value vJoy already holds (which MSFS used to ignore).
      final arm = find.descendant(
          of: find.byKey(const Key('speedbrake')), matching: find.text('ARM'));
      await tester.tap(arm);
      await waitSim(tester, 'spoilers ARM toggled',
          (t) => t.spoilersArmed != start.spoilersArmed);
      await tester.pump(const Duration(milliseconds: 600));
      await tester.tap(arm);
      await waitSim(tester, 'spoilers ARM restored',
          (t) => t.spoilersArmed == start.spoilersArmed);

      // Speedbrake: full, then retracted. The tick (sim) must meet the knob.
      final speedbrake = find.descendant(
          of: find.byKey(const Key('speedbrake')),
          matching: find.byWidgetPredicate(
              (w) => w.runtimeType.toString() == '_SpeedbrakeLever'));
      await tester.drag(speedbrake, const Offset(0, -300));
      await waitSim(tester, 'speedbrake full', (t) => t.spoilersHandle > 0.9);
      await tester.drag(speedbrake, const Offset(0, 300));
      await waitSim(
          tester, 'speedbrake retracted', (t) => t.spoilersHandle < 0.05);
    }

    // Flaps: up, one detent down, back to where they were. On the FBW
    // A32NX the sim index skips 1, which the lever must learn (n/4, not n/5).
    final flaps = find.byKey(const Key('flapLever'));
    await tester.drag(flaps, const Offset(0, -300));
    await waitSim(tester, 'flaps up', (t) => t.flapHandleIndex == 0);
    await tester.pump(const Duration(milliseconds: 600));
    // Multi-detent moves in one drag: the A32NX dropped most taps until each
    // waited for the sim.
    await tester.drag(flaps, const Offset(0, 300));
    await waitSim(tester, 'flaps full in one drag',
        (t) => t.flapHandleIndex == t.flapHandleIndexMax);
    await tester.pump(const Duration(milliseconds: 600));
    await tester.drag(flaps, const Offset(0, -300));
    await waitSim(
        tester, 'flaps up in one drag', (t) => t.flapHandleIndex == 0);
    await tester.pump(const Duration(milliseconds: 600));
    await tester.drag(flaps, const Offset(0, 40));
    final one =
        await waitSim(tester, 'flaps one detent', (t) => t.flapHandleIndex > 0);
    await tester.pump(const Duration(milliseconds: 600));
    if (one.flapHandleIndex == 2) {
      expect(find.text('1/${one.flapHandleIndexMax - 1}'), findsOneWidget);
    }
    await tester.pump(const Duration(milliseconds: 600));
    await tester.drag(flaps, const Offset(0, -300));
    await waitSim(tester, 'flaps up again', (t) => t.flapHandleIndex == 0);
    await tester.pump(const Duration(milliseconds: 600));
    while (sim!.flapHandleIndex < start.flapHandleIndex) {
      final before = sim!.flapHandleIndex;
      await tester.drag(flaps, const Offset(0, 40));
      await waitSim(tester, 'flaps stepping back to ${start.flapHandleIndex}',
          (t) => t.flapHandleIndex > before);
      await tester.pump(const Duration(milliseconds: 600));
    }

    // Parking brake: toggle and back.
    final parking = find.byKey(const Key('parkingBrake'));
    await tester.tap(parking);
    await waitSim(tester, 'parking brake toggled',
        (t) => t.parkingBrakeSet != start.parkingBrakeSet);
    await tester.pump(const Duration(milliseconds: 600));
    await tester.tap(parking);
    await waitSim(tester, 'parking brake restored',
        (t) => t.parkingBrakeSet == start.parkingBrakeSet);

    // Stops the send loop before tearDown disposes the providers it reads.
    await tester.pumpWidget(const SizedBox());
    await SystemChrome.setPreferredOrientations([]);
    await SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);
  });
}

String _describe(proto.Telemetry? t) => t == null
    ? 'none'
    : 'flaps=${t.flapHandleIndex}/${t.flapHandleIndexMax} '
        'spoilers=${t.spoilersHandle.toStringAsFixed(2)} '
        'parkingBrake=${t.parkingBrakeSet} paused=${t.paused}';
