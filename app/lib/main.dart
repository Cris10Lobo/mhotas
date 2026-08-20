import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'ui/connect_screen.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  // Flying screen is laid out landscape-only (see control_screen.dart) —
  // lock rotation so it can't flip to portrait mid-flight.
  SystemChrome.setPreferredOrientations([
    DeviceOrientation.landscapeLeft,
    DeviceOrientation.landscapeRight,
  ]);
  // immersiveSticky over immersive: a stray edge swipe while gripping the
  // phone shouldn't need a second swipe to dismiss the status/nav bars again.
  SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky);
  runApp(const ProviderScope(child: MHotasApp()));
}

class MHotasApp extends StatelessWidget {
  const MHotasApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'mHOTAS',
      theme: ThemeData(colorSchemeSeed: Colors.blue, useMaterial3: true),
      darkTheme: ThemeData(
        colorSchemeSeed: Colors.blue,
        brightness: Brightness.dark,
        useMaterial3: true,
      ),
      home: const ConnectScreen(),
    );
  }
}
