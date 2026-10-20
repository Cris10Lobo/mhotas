// Drives the real ControlScreen — real widgets, real UdpClient, real UDP
// socket — against a tiny fake "server" this test itself runs, and asserts
// on packets decoded with the app's own protocol.dart. That's viable
// headless (no emulator, no device) because UdpClient talks UDP via plain
// dart:io, no platform channel — and a real mhotas-server subprocess isn't
// needed either, since the Go side's wire->backend mapping is already
// covered by server/internal/transport/server_test.go's fakeBackend; this
// file is that same "fake peer, not a real process" pattern mirrored on the
// Dart side, for the gap that pattern doesn't cover: does the app itself
// send the right thing.
import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mhotas/network/protocol.dart' as proto;
import 'package:mhotas/network/udp_client.dart' show ConnectionStatus;
import 'package:mhotas/state/connection.dart';
import 'package:mhotas/state/settings.dart';
import 'package:mhotas/state/telemetry.dart';
import 'package:mhotas/ui/control_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

// Gyroscope/tilt is out of scope here (see control_screen.dart's
// AttitudeStream — motion sensors aren't meaningful off real hardware
// either), so rotation_sensor's channels have no test binding. Mock them to
// succeed as a no-op rather than throw, matching what actually happens on
// a real desktop/CI machine with no rotation sensor.
const _rotationSensorChannels = [
  'rotation_sensor/method',
  'rotation_sensor/orientation'
];

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    for (final name in _rotationSensorChannels) {
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(MethodChannel(name), (call) async => null);
    }
  });

  tearDown(() {
    for (final name in _rotationSensorChannels) {
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(MethodChannel(name), null);
    }
  });

  group('telemetry -> UI', () {
    testWidgets('flap lever shows the synced detent/max', (tester) async {
      await _pumpScreen(tester, overrides: [
        telemetryProvider
            .overrideWith((ref) => Stream.value(const proto.Telemetry(
                  flapHandleIndex: 2,
                  flapHandleIndexMax: 4,
                  gearPosition: proto.GearPosition.up,
                  parkingBrakeSet: false,
                  throttlePercent: 0,
                  paused: false,
                  stallWarning: false,
                  onGround: false,
                  groundSpeedKnots: 0,
                ))),
      ]);
      await tester.pump();
      expect(find.text('2/4'), findsOneWidget);
    });

    testWidgets('throttle lever shows A/T badge only while autothrottle is on',
        (tester) async {
      final controller = StreamController<proto.Telemetry>();
      addTearDown(controller.close);
      await _pumpScreen(tester, overrides: [
        telemetryProvider.overrideWith((ref) => controller.stream),
      ]);
      controller.add(_telemetry(autothrottleActive: true));
      await tester.pump();
      expect(find.text('A/T'), findsOneWidget);

      controller.add(_telemetry());
      await tester.pump();
      await tester.pump();
      expect(find.text('A/T'), findsNothing);
    });

    testWidgets('trim rocker shows the sim\'s elevator trim', (tester) async {
      await _pumpScreen(tester, overrides: [
        telemetryProvider.overrideWith(
            (ref) => Stream.value(_telemetry(elevatorTrim: -0.25))),
      ]);
      await tester.pump();
      expect(find.text('-25%'), findsOneWidget);
    });

    testWidgets('gear toggle bolds DOWN when telemetry says down',
        (tester) async {
      await _pumpScreen(tester, overrides: [
        telemetryProvider
            .overrideWith((ref) => Stream.value(const proto.Telemetry(
                  flapHandleIndex: 0,
                  flapHandleIndexMax: 0,
                  gearPosition: proto.GearPosition.down,
                  parkingBrakeSet: false,
                  throttlePercent: 0,
                  paused: false,
                  stallWarning: false,
                  onGround: false,
                  groundSpeedKnots: 0,
                ))),
      ]);
      await tester.pump();
      final down = tester.widget<Text>(find.text('DOWN'));
      final up = tester.widget<Text>(find.text('UP'));
      expect(down.style?.fontWeight, FontWeight.bold);
      expect(up.style?.fontWeight, isNot(FontWeight.bold));
    });

    testWidgets('nothing fires on the very first telemetry packet',
        (tester) async {
      final controller = StreamController<proto.Telemetry>();
      addTearDown(controller.close);
      final calls = _recordHaptics();
      addTearDown(calls.dispose);

      await _pumpScreen(tester, overrides: [
        telemetryProvider.overrideWith((ref) => controller.stream),
      ]);
      // A first-ever reading of GearPosition.down: with no prior telemetry
      // to compare against, this must not read as a "just locked down"
      // transition.
      controller.add(_telemetry(gearPosition: proto.GearPosition.down));
      await tester.pump();

      expect(calls.haptics, isEmpty);
      expect(calls.vibrations, isEmpty);
    });

    testWidgets(
        'gear/flap/parking-brake/pause fire one-shot haptics only on confirmed transitions',
        (tester) async {
      final controller = StreamController<proto.Telemetry>();
      addTearDown(controller.close);
      final calls = _recordHaptics();
      addTearDown(calls.dispose);

      await _pumpScreen(tester, overrides: [
        telemetryProvider.overrideWith((ref) => controller.stream),
      ]);
      controller.add(_telemetry(gearPosition: proto.GearPosition.transit));
      await tester.pump();
      calls.haptics.clear(); // first packet — nothing to assert on twice

      controller.add(_telemetry(gearPosition: proto.GearPosition.down));
      await tester.pump();
      expect(calls.haptics, ['HapticFeedbackType.heavyImpact']);
      calls.haptics.clear();

      // Entering transit on the way back up: no haptic — only the locked
      // end states buzz.
      controller.add(_telemetry(gearPosition: proto.GearPosition.transit));
      await tester.pump();
      expect(calls.haptics, isEmpty);

      controller.add(_telemetry(gearPosition: proto.GearPosition.up));
      await tester.pump();
      expect(calls.haptics, ['HapticFeedbackType.mediumImpact']);
      calls.haptics.clear();

      controller.add(
          _telemetry(gearPosition: proto.GearPosition.up, flapHandleIndex: 1));
      await tester.pump();
      expect(calls.haptics, ['HapticFeedbackType.lightImpact']);
      calls.haptics.clear();

      controller.add(_telemetry(
          gearPosition: proto.GearPosition.up,
          flapHandleIndex: 1,
          parkingBrakeSet: true));
      await tester.pump();
      expect(calls.haptics, ['HapticFeedbackType.selectionClick']);
      calls.haptics.clear();

      controller.add(_telemetry(
          gearPosition: proto.GearPosition.up,
          flapHandleIndex: 1,
          parkingBrakeSet: true,
          paused: true));
      await tester.pump();
      expect(calls.haptics, ['HapticFeedbackType.lightImpact']);
    });

    testWidgets('stall warning starts and stops a repeating vibration',
        (tester) async {
      final controller = StreamController<proto.Telemetry>();
      addTearDown(controller.close);
      final calls = _recordHaptics();
      addTearDown(calls.dispose);

      await _pumpScreen(tester, overrides: [
        telemetryProvider.overrideWith((ref) => controller.stream),
      ]);
      controller.add(_telemetry());
      await tester.pump();
      expect(calls.vibrations, isEmpty);

      controller.add(_telemetry(stallWarning: true));
      await tester.pump();
      expect(calls.vibrations, ['vibrate']);
      expect(calls.lastVibratePattern, [0, 130, 90]);
      expect(calls.lastVibrateRepeat, 0);
      calls.vibrations.clear();

      // Still active on the next packet — must not restart the loop.
      controller.add(_telemetry(stallWarning: true));
      await tester.pump();
      expect(calls.vibrations, isEmpty);

      controller.add(_telemetry(stallWarning: false));
      await tester.pump();
      expect(calls.vibrations, ['cancel']);
    });

    testWidgets('sim pause stops stall shaker and taxi rumble', (tester) async {
      // Regression test: paused telemetry keeps reporting the last
      // stallWarning/onGround/groundSpeedKnots values (the sim freezes
      // rather than clearing them), so both loops used to keep buzzing
      // straight through a pause.
      final controller = StreamController<proto.Telemetry>();
      addTearDown(controller.close);
      final calls = _recordHaptics();
      addTearDown(calls.dispose);

      await _pumpScreen(tester, overrides: [
        telemetryProvider.overrideWith((ref) => controller.stream),
      ]);
      controller.add(
          _telemetry(stallWarning: true, onGround: true, groundSpeedKnots: 20));
      await tester.pump();
      expect(calls.vibrations, ['vibrate', 'vibrate']); // shaker + rumble
      calls.haptics.clear();
      calls.vibrations.clear();

      controller.add(_telemetry(
          paused: true,
          stallWarning: true,
          onGround: true,
          groundSpeedKnots: 20));
      await tester.pump();
      expect(calls.vibrations, ['cancel', 'cancel']);
      calls.vibrations.clear();

      // Unpausing with the same sim state still active must restart both.
      controller.add(
          _telemetry(stallWarning: true, onGround: true, groundSpeedKnots: 20));
      await tester.pump();
      expect(calls.vibrations, ['vibrate', 'vibrate']);
    });

    testWidgets(
        'taxi rumble switches pattern by ground-speed band and stops airborne',
        (tester) async {
      final controller = StreamController<proto.Telemetry>();
      addTearDown(controller.close);
      final calls = _recordHaptics();
      addTearDown(calls.dispose);

      await _pumpScreen(tester, overrides: [
        telemetryProvider.overrideWith((ref) => controller.stream),
      ]);
      controller.add(_telemetry());
      await tester.pump();

      // Parked/stationary — below the lowest band, no rumble.
      controller.add(_telemetry(onGround: true, groundSpeedKnots: 1));
      await tester.pump();
      expect(calls.vibrations, isEmpty);

      controller.add(_telemetry(onGround: true, groundSpeedKnots: 10));
      await tester.pump();
      expect(calls.vibrations, ['vibrate']);
      calls.vibrations.clear();

      // Same band — must not restart the pattern on every packet.
      controller.add(_telemetry(onGround: true, groundSpeedKnots: 12));
      await tester.pump();
      expect(calls.vibrations, isEmpty);

      controller.add(_telemetry(onGround: true, groundSpeedKnots: 20));
      await tester.pump();
      expect(calls.vibrations, ['vibrate']); // band changed, pattern restarts
      calls.vibrations.clear();

      controller.add(_telemetry(onGround: false, groundSpeedKnots: 20));
      await tester.pump();
      expect(calls.vibrations, ['cancel']); // airborne — rumble stops
    });

    testWidgets('pause icon reflects telemetry.paused', (tester) async {
      await _pumpScreen(tester, overrides: [
        telemetryProvider
            .overrideWith((ref) => Stream.value(const proto.Telemetry(
                  flapHandleIndex: 0,
                  flapHandleIndexMax: 0,
                  gearPosition: proto.GearPosition.up,
                  parkingBrakeSet: false,
                  throttlePercent: 0,
                  paused: true,
                  stallWarning: false,
                  onGround: false,
                  groundSpeedKnots: 0,
                ))),
      ]);
      await tester.pump();
      // find.byIcon alone is ambiguous: the tilt indicator has its own,
      // unrelated pause/play icon (gyroPausedProvider) using the same
      // Icons.pause/play_arrow pair — go through the AppBar button
      // specifically, by its stable tooltip.
      final icon = tester.widget<Icon>(find.descendant(
          of: find.byTooltip('Pause'), matching: find.byType(Icon)));
      expect(icon.icon, Icons.play_arrow);
    });
  });

  group('buttons/axes -> wire protocol', () {
    testWidgets('momentary and toggle buttons send the right BUTTON packets',
        (tester) async {
      await tester.runAsync(() async {
        final server = await _FakeServer.start();
        addTearDown(server.close);
        final container = ProviderContainer();
        addTearDown(container.dispose);

        await _pumpScreen(tester, container: container);
        await _connect(tester, container, server);

        Future<void> expectTap(Finder finder, int buttonId) async {
          await tester.tap(finder);
          final press = await server.nextButton();
          expect(press.buttonId, buttonId);
          expect(press.pressed, isTrue);
          final release = await server.nextButton();
          expect(release.buttonId, buttonId);
          expect(release.pressed, isFalse);
        }

        await expectTap(find.byKey(const Key('gear')), ButtonIds.gear);
        await expectTap(
            find.byKey(const Key('parkingBrake')), ButtonIds.parkingBrake);
        await expectTap(find.byTooltip('Pause'), ButtonIds.pause);
        await expectTap(find.byKey(const Key('toeBrake')), ButtonIds.toeBrake);
        await expectTap(find.byKey(const Key('autopilotDisconnect')),
            ButtonIds.autopilotDisconnect);
        await expectTap(
            find.descendant(
                of: find.byKey(const Key('speedbrake')),
                matching: find.text('ARM')),
            ButtonIds.spoilerArm);
      });
    });

    testWidgets('flap lever drag sends incr then decr taps', (tester) async {
      await tester.runAsync(() async {
        final server = await _FakeServer.start();
        addTearDown(server.close);
        final container = ProviderContainer(overrides: [
          telemetryProvider
              .overrideWith((ref) => Stream.value(const proto.Telemetry(
                    flapHandleIndex: 0,
                    flapHandleIndexMax: 4,
                    gearPosition: proto.GearPosition.up,
                    parkingBrakeSet: false,
                    throttlePercent: 0,
                    paused: false,
                    stallWarning: false,
                    onGround: false,
                    groundSpeedKnots: 0,
                  ))),
        ]);
        addTearDown(container.dispose);

        await _pumpScreen(tester, container: container);
        await tester.pump(); // let the telemetry override reach the lever
        await _connect(tester, container, server);

        await tester.drag(
            find.byKey(const Key('flapLever')), const Offset(0, 50));
        final incr = await server.nextButton();
        expect(incr.buttonId, ButtonIds.flapIncr);
        expect((await server.nextButton()).pressed, isFalse); // release

        await tester.drag(
            find.byKey(const Key('flapLever')), const Offset(0, -50));
        final decr = await server.nextButton();
        expect(decr.buttonId, ButtonIds.flapDecr);
        expect((await server.nextButton()).pressed, isFalse); // release
      });
    });

    testWidgets('speedbrake lever drag sends incr then decr taps',
        (tester) async {
      await tester.runAsync(() async {
        final server = await _FakeServer.start();
        addTearDown(server.close);
        final container = ProviderContainer();
        addTearDown(container.dispose);

        await _pumpScreen(tester, container: container);
        await _connect(tester, container, server);

        final lever = find.descendant(
            of: find.byKey(const Key('speedbrake')),
            matching: find.byWidgetPredicate(
                (w) => w.runtimeType.toString() == '_SpeedbrakeLever'));

        await tester.drag(lever, const Offset(0, -80));
        final incr = await server.nextButton();
        expect(incr.buttonId, ButtonIds.spoilerIncr);
        expect((await server.nextButton()).pressed, isFalse); // release

        await tester.drag(lever, const Offset(0, 80));
        final decr = await server.nextButton();
        expect(decr.buttonId, ButtonIds.spoilerDecr);
        expect((await server.nextButton()).pressed, isFalse); // release
      });
    });

    testWidgets('trim rocker sends up then down, each press+release',
        (tester) async {
      await tester.runAsync(() async {
        final server = await _FakeServer.start();
        addTearDown(server.close);
        final container = ProviderContainer();
        addTearDown(container.dispose);

        await _pumpScreen(tester, container: container);
        await _connect(tester, container, server);

        final up = find.descendant(
            of: find.byKey(const Key('trim')), matching: find.text('▲'));
        await tester.tap(up);
        expect((await server.nextButton()).buttonId, ButtonIds.trimUp);
        expect((await server.nextButton()).pressed, isFalse);

        final down = find.descendant(
            of: find.byKey(const Key('trim')), matching: find.text('▼'));
        await tester.tap(down);
        expect((await server.nextButton()).buttonId, ButtonIds.trimDown);
        expect((await server.nextButton()).pressed, isFalse);
      });
    });

    testWidgets('nudge-throttle mode sends incr/decr/reverse-arm',
        (tester) async {
      await tester.runAsync(() async {
        final server = await _FakeServer.start();
        addTearDown(server.close);
        final container = ProviderContainer();
        addTearDown(container.dispose);

        await _pumpScreen(tester, container: container);
        container.read(nudgeThrottleProvider.notifier).setEnabled(true);
        await tester.pump();
        await _connect(tester, container, server);

        final incr = find.descendant(
            of: find.byKey(const Key('throttle')), matching: find.text('+'));
        await tester.tap(incr);
        expect((await server.nextButton()).buttonId, ButtonIds.throttleIncr);
        expect((await server.nextButton()).pressed, isFalse);

        final decr = find.descendant(
            of: find.byKey(const Key('throttle')), matching: find.text('-'));
        await tester.tap(decr);
        expect((await server.nextButton()).buttonId, ButtonIds.throttleDecr);
        expect((await server.nextButton()).pressed, isFalse);

        final reverseArm = find.descendant(
            of: find.byKey(const Key('throttle')), matching: find.text('REV'));
        await tester.tap(reverseArm);
        final press = await server.nextButton();
        expect(press.buttonId, ButtonIds.throttleReverseArm);
        expect(press.pressed, isTrue);
        final release = await server.nextButton();
        expect(release.pressed, isFalse);
      });
    });

    testWidgets('throttle lever and rudder stick send AXIS packets',
        (tester) async {
      await tester.runAsync(() async {
        final server = await _FakeServer.start();
        addTearDown(server.close);
        final container = ProviderContainer();
        addTearDown(container.dispose);

        await _pumpScreen(tester, container: container);
        await _connect(tester, container, server);

        await tester.drag(
            find.byKey(const Key('throttle')), const Offset(0, -100));
        final axis1 =
            await server.nextAxis(where: (a) => a.throttle.abs() > 0.05);
        expect(axis1.throttle, greaterThan(0));

        await tester.drag(find.byKey(const Key('rudder')), const Offset(80, 0));
        final axis2 = await server.nextAxis(where: (a) => a.rudder.abs() > 0.1);
        expect(axis2.rudder, greaterThan(0));

        // Let the throttle/rudder spring-back animations finish before the
        // container gets disposed below — otherwise a ticking
        // AnimationController's listener can still fire (calling ref.read)
        // after the container is gone.
        await tester.pumpAndSettle();
      });
    });

    testWidgets('manual joystick sends pitch/roll AXIS packets',
        (tester) async {
      await tester.runAsync(() async {
        final server = await _FakeServer.start();
        addTearDown(server.close);
        final container = ProviderContainer();
        addTearDown(container.dispose);

        await _pumpScreen(tester, container: container);
        container.read(manualJoystickProvider.notifier).setEnabled(true);
        await tester.pump();
        await _connect(tester, container, server);

        await tester.drag(
            find.byKey(const Key('attitude')), const Offset(60, 40));
        final axis = await server.nextAxis(
            where: (a) => a.roll.abs() > 0.1 && a.pitch.abs() > 0.1);
        expect(axis.roll, greaterThan(0));
        expect(axis.pitch, greaterThan(0));

        await tester.pumpAndSettle(); // let the spring-back animation finish
      });
    });
  });
}

/// Builds a Telemetry with sensible defaults, overriding only the fields a
/// haptics test cares about — callers must repeat any field they want held
/// steady across a sequence of packets, since each call is independent.
proto.Telemetry _telemetry({
  int flapHandleIndex = 0,
  int flapHandleIndexMax = 4,
  int gearPosition = proto.GearPosition.up,
  bool parkingBrakeSet = false,
  double throttlePercent = 0,
  bool paused = false,
  bool stallWarning = false,
  bool onGround = false,
  double groundSpeedKnots = 0,
  double elevatorTrim = 0,
  bool autothrottleActive = false,
}) =>
    proto.Telemetry(
      flapHandleIndex: flapHandleIndex,
      flapHandleIndexMax: flapHandleIndexMax,
      gearPosition: gearPosition,
      parkingBrakeSet: parkingBrakeSet,
      throttlePercent: throttlePercent,
      paused: paused,
      stallWarning: stallWarning,
      onGround: onGround,
      groundSpeedKnots: groundSpeedKnots,
      elevatorTrim: elevatorTrim,
      autothrottleActive: autothrottleActive,
    );

/// Records every HapticFeedback (flutter/platform channel) and vibration
/// package (its own 'vibration' channel) method call made while installed,
/// so a test can assert on exactly what fired without a real device.
class _HapticRecorder {
  _HapticRecorder() {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(SystemChannels.platform, (call) async {
      // Every HapticFeedback.* static method funnels through the single
      // 'HapticFeedback.vibrate' platform method, with the actual variant
      // passed as the string argument (e.g. 'HapticFeedbackType.heavyImpact')
      // — see flutter/services/haptic_feedback.dart. Record the variant, not
      // the (always-identical) method name.
      if (call.method == 'HapticFeedback.vibrate') {
        haptics.add(call.arguments as String? ?? 'HapticFeedbackType.vibrate');
      }
      return null;
    });
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(const MethodChannel('vibration'),
            (call) async {
      vibrations.add(call.method);
      if (call.method == 'vibrate') {
        final args = call.arguments as Map;
        lastVibratePattern = List<int>.from(args['pattern'] as List);
        lastVibrateRepeat = args['repeat'] as int;
      }
      return null;
    });
  }

  final haptics = <String>[];
  final vibrations = <String>[];
  List<int>? lastVibratePattern;
  int? lastVibrateRepeat;

  void dispose() {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(SystemChannels.platform, null);
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(const MethodChannel('vibration'), null);
  }
}

_HapticRecorder _recordHaptics() => _HapticRecorder();

/// Pumps ControlScreen inside a real-sized MaterialApp so the landscape
/// control row fits without overflow. Callers either pass [container]
/// directly (so they can also call `container.read` afterward, e.g. to
/// connect the UdpClient) or [overrides] for a fresh one.
Future<void> _pumpScreen(
  WidgetTester tester, {
  ProviderContainer? container,
  List<Override> overrides = const [],
}) async {
  await tester.binding.setSurfaceSize(const Size(1400, 900));
  addTearDown(() => tester.binding.setSurfaceSize(null));
  final c = container ?? ProviderContainer(overrides: overrides);
  if (container == null) addTearDown(c.dispose);
  await tester.pumpWidget(UncontrolledProviderScope(
    container: c,
    child: const MaterialApp(home: ControlScreen()),
  ));
}

/// Connects the real UdpClient to [server] and waits for HELLO_ACK to land
/// (a real round-trip, not an arbitrary delay), then flushes the resulting
/// rebuild.
Future<void> _connect(WidgetTester tester, ProviderContainer container,
    _FakeServer server) async {
  container.read(udpClientProvider).connect('127.0.0.1', server.port);
  final deadline = DateTime.now().add(const Duration(seconds: 2));
  while (container.read(connectionStatusProvider).valueOrNull !=
      ConnectionStatus.connected) {
    if (DateTime.now().isAfter(deadline)) {
      throw TimeoutException('never reached ConnectionStatus.connected');
    }
    await Future.delayed(const Duration(milliseconds: 5));
  }
  await tester.pump();
}

/// Stands in for mhotas-server: replies to HELLO with a hand-built
/// HELLO_ACK (protocol.dart has no encodeHelloAck — only the Go server ever
/// sends one) and queues every other packet for the test to decode with the
/// app's own protocol.dart.
class _FakeServer {
  _FakeServer._(this._socket);

  final RawDatagramSocket _socket;
  final _queue = _PacketQueue();
  // UdpClient.sendButton fires each event 3x redundantly (see
  // udp_client.dart) so a dropped packet doesn't lose it — the real server
  // dedupes by eventId (transport.go's handleButton); this fake one dedupes
  // by (buttonId, pressed) instead, close enough since nothing here sends
  // the same edge twice on purpose.
  (int, bool)? _lastButton;

  static Future<_FakeServer> start() async {
    final socket =
        await RawDatagramSocket.bind(InternetAddress.loopbackIPv4, 0);
    final server = _FakeServer._(socket);
    socket.listen((event) {
      if (event != RawSocketEvent.read) return;
      final dg = socket.receive();
      if (dg == null) return;
      final data = dg.data;
      if (data.isNotEmpty && data[0] == proto.PacketType.hello) {
        socket.send(_helloAckBytes(), dg.address, dg.port);
        return;
      }
      server._queue.add(data);
    });
    return server;
  }

  int get port => _socket.port;

  Future<proto.Button> nextButton() async {
    while (true) {
      final data = await _queue.next();
      if (data[0] != proto.PacketType.button) continue;
      final b = proto.decodeButton(data);
      final key = (b.buttonId, b.pressed);
      if (key == _lastButton) continue; // one of the 3 redundant resends
      _lastButton = key;
      return b;
    }
  }

  Future<proto.Axis> nextAxis({bool Function(proto.Axis)? where}) async {
    while (true) {
      final data = await _queue.next();
      if (data[0] != proto.PacketType.axis) continue;
      final axis = proto.decodeAxis(data);
      if (where == null || where(axis)) return axis;
    }
  }

  void close() => _socket.close();
}

Uint8List _helloAckBytes() {
  // header(4) + status(1) + name field(32); status 0 = HelloOK, name left
  // zeroed — nothing here reads the server's name.
  final buf = Uint8List(4 + 1 + proto.nameFieldSize);
  buf[0] = proto.PacketType.helloAck;
  buf[1] = proto.ProtocolVersion.current;
  return buf;
}

/// Minimal ordered async queue: callers may await [next] before or after
/// the matching [add] arrives — needed since the fake server's socket
/// listener and the test's awaits run concurrently in real time.
class _PacketQueue {
  final _pending = <Uint8List>[];
  Completer<Uint8List>? _waiting;

  void add(Uint8List data) {
    final waiting = _waiting;
    if (waiting != null) {
      _waiting = null;
      waiting.complete(data);
    } else {
      _pending.add(data);
    }
  }

  Future<Uint8List> next() {
    if (_pending.isNotEmpty) return Future.value(_pending.removeAt(0));
    final completer = Completer<Uint8List>();
    _waiting = completer;
    return completer.future.timeout(const Duration(seconds: 2));
  }
}
