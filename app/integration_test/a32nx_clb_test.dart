// With autopilot and autothrust flying the FBW A32NX, a lever put in CLB must
// stay there, on the phone and in the sim, however autothrust changes the
// thrust. Reading the stock throttle simvar, which follows the thrust, moved it.
//
//   flutter test integration_test/a32nx_clb_test.dart -d <device> \
//     --dart-define=MHOTAS_SERVER=192.168.0.101:9750
//
// Needs the A32NX airborne, unpaused, autopilot on and the A/THR pushbutton on.
// While the hold runs, change the selected speed on the FCU by 20 kt or more so
// autothrust moves the thrust; the test can't do that itself, and without it
// nothing tries to move the lever. Run `make probe` with -watch on the stock
// `GENERAL ENG THROTTLE LEVER POSITION:1,percent` alongside to confirm it did:
// TELEMETRY can't show the stock reading.
//
// Leaves the lever in CLB, since this runs in flight. Tilt is paused so the
// phone's attitude can't disconnect the autopilot.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:mhotas/network/protocol.dart' as proto;
import 'package:mhotas/network/udp_client.dart';
import 'package:mhotas/state/connection.dart';
import 'package:mhotas/state/gyro_pause.dart';
import 'package:mhotas/ui/control_screen.dart';

const _server = String.fromEnvironment('MHOTAS_SERVER');

// The profile's throttle_quadrant reading of 50 (idle 25, full forward 100).
const _clb = 1 / 3;
const _hold = Duration(seconds: 60);

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  late ProviderContainer container;
  proto.Telemetry? sim;

  setUp(() async {
    expect(_server, isNotEmpty, reason: '--dart-define=MHOTAS_SERVER=ip:port');
    container = ProviderContainer();
    container.read(gyroPausedProvider.notifier).state = true;
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

  testWidgets('lever stays in CLB under autothrust', (tester) async {
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
    final knob = find.descendant(
        of: throttle,
        matching: find.byWidgetPredicate(
            (w) => w is Container && w.constraints?.maxHeight == 36));
    double knobY() => tester.getCenter(knob.first).dy;

    await pumpFor(const Duration(seconds: 1));
    expect(sim, isNotNull, reason: 'no telemetry');
    expect(sim!.paused, isFalse, reason: 'unpause the sim first');
    expect(sim!.onGround, isFalse, reason: 'needs the aircraft airborne');
    expect(sim!.throttlePercent, greaterThanOrEqualTo(0),
        reason: 'lever reads reverse: is the A32NX profile loaded?');

    // Start above the detent so the phone is what puts the lever in CLB.
    // In steps, since the detent spans a good part of the phone's travel.
    for (var i = 0; i < 8 && (sim!.throttlePercent - _clb).abs() < 0.02; i++) {
      await tester.drag(throttle, const Offset(0, -20));
      await pumpFor(const Duration(seconds: 2));
      debugPrint('CLB leaving step=$i sim=${sim!.throttlePercent}');
    }
    expect((sim!.throttlePercent - _clb).abs(), greaterThanOrEqualTo(0.02),
        reason: 'lever did not leave CLB: is the phone reaching the sim?');

    // Closed loop: which phone value lands in the detent depends on the
    // calibration and the aircraft's throttle configuration.
    for (var i = 0;
        i < 40 && (sim!.throttlePercent - _clb).abs() >= 0.02;
        i++) {
      final error = _clb - sim!.throttlePercent;
      final px = (error * 150).abs().clamp(4.0, 40.0);
      await tester.drag(throttle, Offset(0, error > 0 ? -px : px));
      await pumpFor(const Duration(milliseconds: 1500));
    }
    expect((sim!.throttlePercent - _clb).abs(), lessThan(0.02),
        reason: 'lever never reached CLB, at ${sim!.throttlePercent}');

    // Past the settle time, so the hold doesn't measure the lever arriving.
    await pumpFor(const Duration(seconds: 2));
    final simStart = sim!.throttlePercent;
    final phoneStart = knobY();
    debugPrint('CLB reached sim=${simStart.toStringAsFixed(3)}; '
        'change the FCU speed now');

    var simDrift = 0.0, phoneDrift = 0.0;
    var autopilotHeld = true, autothrottleHeld = true;
    final end = DateTime.now().add(_hold);
    while (DateTime.now().isBefore(end)) {
      await pumpFor(const Duration(milliseconds: 500));
      final simDelta = (sim!.throttlePercent - simStart).abs();
      final phoneDelta = (knobY() - phoneStart).abs();
      simDrift = simDelta > simDrift ? simDelta : simDrift;
      phoneDrift = phoneDelta > phoneDrift ? phoneDelta : phoneDrift;
      autopilotHeld &= sim!.autopilotMaster;
      autothrottleHeld &= sim!.autothrottleActive;
      debugPrint('CLB sim=${sim!.throttlePercent.toStringAsFixed(3)} '
          'knobMovedPx=${phoneDelta.toStringAsFixed(1)} '
          'at=${sim!.autothrottleActive}');
    }
    debugPrint('CLB summary simDrift=${simDrift.toStringAsFixed(3)} '
        'phoneDriftPx=${phoneDrift.toStringAsFixed(1)} '
        'autopilotHeld=$autopilotHeld autothrottleHeld=$autothrottleHeld');

    await tester.pumpWidget(const SizedBox());
    await SystemChrome.setPreferredOrientations([]);
    await SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);

    expect(simDrift, lessThan(0.02), reason: 'sim lever left CLB');
    expect(phoneDrift, lessThan(3), reason: 'phone lever left where it was');
    expect(autopilotHeld, isTrue, reason: 'autopilot disconnected');
    expect(autothrottleHeld, isTrue, reason: 'autothrust not active in CLB');
  });
}
