// Exercises every control on the flying screen against a live sim and logs
// what TELEMETRY reported before and after each step. Never fails on a step:
// it is a survey, read alongside the server log and a `simprobe -watch`
// recording (step markers carry wall-clock times to line them up):
//
//   flutter test integration_test/survey_test.dart -d <device> \
//     --dart-define=MHOTAS_SERVER=192.168.0.101:9750
//
// Needs the aircraft on the ground with the parking brake set. Leaves gear
// alone (retracting it on the ground can collapse the aircraft) and restores
// flaps, parking brake, pause and the app's settings.
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:mhotas/network/protocol.dart' as proto;
import 'package:mhotas/network/udp_client.dart';
import 'package:mhotas/state/connection.dart';
import 'package:mhotas/state/settings.dart';
import 'package:mhotas/ui/control_screen.dart';

const _server = String.fromEnvironment('MHOTAS_SERVER');

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('survey every control', (tester) async {
    final container = ProviderContainer();
    proto.Telemetry? sim;
    final client = container.read(udpClientProvider);
    client.telemetry.listen((t) => sim = t);

    // Real time, unlike tester.pump(duration) in some bindings.
    Future<void> wait(int ms) async {
      final end = DateTime.now().add(Duration(milliseconds: ms));
      while (DateTime.now().isBefore(end)) {
        await tester
            .runAsync(() => Future.delayed(const Duration(milliseconds: 50)));
        await tester.pump();
      }
    }

    String now() => DateTime.now().toIso8601String().substring(11, 23);

    Future<void> step(String name, Future<void> Function() act,
        {int settleMs = 3000}) async {
      final before = _describe(sim);
      debugPrint('STEP $name start=${now()}');
      try {
        await act();
        await wait(settleMs);
        debugPrint('STEP $name end=${now()}\n  before: $before\n  after:  '
            '${_describe(sim)}');
      } catch (e) {
        debugPrint('STEP $name ERROR $e');
      }
    }

    Future<void> hold(Finder f, Offset by, int ms) async {
      final g = await tester.startGesture(tester.getCenter(f));
      // Several moves so the recognizer passes its touch slop first.
      for (var i = 0; i < 5; i++) {
        await g.moveBy(by / 5);
        await tester.pump();
      }
      await wait(ms);
      await g.up();
      await tester.pump();
    }

    Future<void> press(Finder f, int ms) async {
      final g = await tester.startGesture(tester.getCenter(f));
      await wait(ms);
      await g.up();
      await tester.pump();
    }

    final connected = client.status
        .firstWhere((s) => s == ConnectionStatus.connected)
        .timeout(const Duration(seconds: 5));
    final [host, port] = _server.split(':');
    await tester.runAsync(() async {
      await client.connect(host, int.parse(port));
      await connected;
      await Future.delayed(const Duration(seconds: 1));
    });
    final start = sim!;
    debugPrint('CONNECTED ${now()} sim before the screen sends anything: '
        '${_describe(start)}');

    // As main() does; in portrait half the controls lay out off-screen.
    await SystemChrome.setPreferredOrientations(
        [DeviceOrientation.landscapeLeft]);
    await wait(1500);
    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: ControlScreen()),
    ));
    await wait(2000);
    debugPrint('MOUNTED ${now()} sim 2s after the screen started sending: '
        '${_describe(sim)}');

    try {
      final pause = find.byTooltip('Pause');
      await step('pause on', () => tester.tap(pause));
      await step('pause off', () => tester.tap(pause));

      final parking = find.byKey(const Key('parkingBrake'));
      await step('parking brake toggle', () => tester.tap(parking));
      await step('parking brake toggle back', () => tester.tap(parking));

      final speedbrake = find.descendant(
          of: find.byKey(const Key('speedbrake')),
          matching: find.byWidgetPredicate(
              (w) => w.runtimeType.toString() == '_SpeedbrakeLever'));
      await step('speedbrake full',
          () => tester.drag(speedbrake, const Offset(0, -300)),
          settleMs: 6000);
      await step(
          'speedbrake half', () => tester.drag(speedbrake, const Offset(0, 80)),
          settleMs: 6000);
      await step('speedbrake retract',
          () => tester.drag(speedbrake, const Offset(0, 300)),
          settleMs: 6000);

      final arm = find.descendant(
          of: find.byKey(const Key('speedbrake')), matching: find.text('ARM'));
      await step('spoiler ARM tap', () => tester.tap(arm));
      await step('spoiler ARM tap back', () => tester.tap(arm));

      final flaps = find.byKey(const Key('flapLever'));
      await step('flaps up', () => tester.drag(flaps, const Offset(0, -300)),
          settleMs: 5000);
      for (var i = 1; i <= start.flapHandleIndexMax; i++) {
        await step('flaps detent +1 (#$i)',
            () => tester.drag(flaps, const Offset(0, 36)),
            settleMs: 4000);
      }
      await step(
          'flaps up again', () => tester.drag(flaps, const Offset(0, -300)),
          settleMs: 6000);
      for (var i = 0;
          i < start.flapHandleIndexMax &&
              sim!.flapHandleIndex < start.flapHandleIndex;
          i++) {
        await step(
            'flaps restore +1', () => tester.drag(flaps, const Offset(0, 36)),
            settleMs: 4000);
      }

      final trim = find.byKey(const Key('trim'));
      final up = find.descendant(of: trim, matching: find.text('▲'));
      final down = find.descendant(of: trim, matching: find.text('▼'));
      await step('trim up x3', () async {
        for (var i = 0; i < 3; i++) {
          await tester.tap(up);
          await wait(300);
        }
      });
      await step('trim down x3', () async {
        for (var i = 0; i < 3; i++) {
          await tester.tap(down);
          await wait(300);
        }
      });

      // With the parking brake set, the brakes already read applied.
      await step('parking brake off for toe brake', () => tester.tap(parking));
      await step('toe brake hold 2s',
          () => press(find.byKey(const Key('toeBrake')), 2000));
      await step('parking brake back on', () => tester.tap(parking));
      await step('AP disconnect tap',
          () => tester.tap(find.byKey(const Key('autopilotDisconnect'))));

      final throttle = find.byKey(const Key('throttle'));
      await step('throttle to ~30% for 4s, then idle', () async {
        await hold(throttle, const Offset(0, -72), 4000);
        await tester.drag(throttle, const Offset(0, 80));
      }, settleMs: 5000);
      await step('throttle reverse hold 3s (springs to idle on release)',
          () => hold(throttle, const Offset(0, 300), 3000),
          settleMs: 5000);

      await container.read(nudgeThrottleProvider.notifier).setEnabled(true);
      await wait(500);
      final nudge = find.byKey(const Key('throttle'));
      await step('nudge + x3', () async {
        for (var i = 0; i < 3; i++) {
          await tester
              .tap(find.descendant(of: nudge, matching: find.text('+')));
          await wait(300);
        }
      });
      await step('nudge - x3', () async {
        for (var i = 0; i < 3; i++) {
          await tester
              .tap(find.descendant(of: nudge, matching: find.text('-')));
          await wait(300);
        }
      });
      await step(
          'nudge REV hold 2s',
          () => press(
              find.descendant(of: nudge, matching: find.text('REV')), 2000),
          settleMs: 5000);
      await container.read(nudgeThrottleProvider.notifier).setEnabled(false);
      await wait(500);

      final rudder = find.byKey(const Key('rudder'));
      await step('rudder hold right 2s',
          () => hold(rudder, const Offset(100, 0), 2000));

      await container.read(manualJoystickProvider.notifier).setEnabled(true);
      await wait(500);
      final stick = find.descendant(
          of: find.byKey(const Key('attitude')),
          matching: find.byWidgetPredicate(
              (w) => w.runtimeType.toString() == '_ManualJoystick'));
      await step('joystick hold up-right 2s',
          () => hold(stick, const Offset(60, -60), 2000));

      await step('failsafe: hold rudder + stick, then stop sending', () async {
        final r = await tester.startGesture(tester.getCenter(rudder));
        final s = await tester.startGesture(tester.getCenter(stick));
        for (var i = 0; i < 5; i++) {
          await r.moveBy(const Offset(20, 0));
          await s.moveBy(const Offset(12, -12));
          await tester.pump();
        }
        await wait(1500);
        debugPrint('  deflected at ${now()}: ${_describe(sim)}');
        await tester.pumpWidget(const SizedBox());
        await r.up();
        await s.up();
      });
    } finally {
      await container.read(manualJoystickProvider.notifier).setEnabled(false);
      await container.read(nudgeThrottleProvider.notifier).setEnabled(false);
      await tester.pumpWidget(const SizedBox());
      await SystemChrome.setPreferredOrientations([]);
      if (sim != null) {
        debugPrint('END ${now()}\n  start: ${_describe(start)}\n  end:   '
            '${_describe(sim)}');
      }
      container.dispose();
    }
  });
}

String _describe(proto.Telemetry? t) => t == null
    ? 'none'
    : 'flaps=${t.flapHandleIndex}/${t.flapHandleIndexMax} '
        'gear=${t.gearPosition} '
        'throttle=${t.throttlePercent.toStringAsFixed(2)} '
        'spoilers=${t.spoilersHandle.toStringAsFixed(2)} '
        'armed=${t.spoilersArmed} '
        'trim=${t.elevatorTrim.toStringAsFixed(3)} '
        'parkingBrake=${t.parkingBrakeSet} paused=${t.paused} '
        'ap=${t.autopilotMaster} at=${t.autothrottleActive} '
        'gs=${t.groundSpeedKnots.toStringAsFixed(1)}';
