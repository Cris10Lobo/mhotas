import 'package:shared_preferences/shared_preferences.dart';

import '../network/protocol.dart' as proto;

/// Which live simvar a control_screen status tile shows. Kept as a UI-only
/// concept (not part of protocol.dart) since it's purely a display choice,
/// not anything sent over the wire.
enum StatusTileKind { airspeed, altitude, heading, verticalSpeed }

/// Number of configurable tiles on the control screen.
const int statusTileCount = 4;

/// One tile per new TELEMETRY v2 field, in wire order, by default.
const List<StatusTileKind> defaultStatusTileKinds = [
  StatusTileKind.airspeed,
  StatusTileKind.altitude,
  StatusTileKind.heading,
  StatusTileKind.verticalSpeed,
];

const _prefsKeyPrefix = 'mhotas.statusTile.';

extension StatusTileKindDisplay on StatusTileKind {
  String get label {
    switch (this) {
      case StatusTileKind.airspeed:
        return 'SPD';
      case StatusTileKind.altitude:
        return 'ALT';
      case StatusTileKind.heading:
        return 'HDG';
      case StatusTileKind.verticalSpeed:
        return 'V/S';
    }
  }

  /// Formats this tile's value out of a TELEMETRY reading, or '--' if none
  /// has arrived yet.
  String valueOf(proto.Telemetry? t) {
    if (t == null) return '--';
    final raw = switch (this) {
      StatusTileKind.airspeed => t.airspeedKnots,
      StatusTileKind.altitude => t.altitudeFeet,
      StatusTileKind.heading => t.headingDegrees,
      StatusTileKind.verticalSpeed => t.verticalSpeedFpm,
    };
    // Crashed live: double.round() throws on NaN/Infinity, and a real sim
    // can report exactly that for some simvars in certain states (e.g.
    // before the flight model has fully loaded) — treat it the same as no
    // reading yet rather than taking the whole screen down.
    if (!raw.isFinite) return '--';
    return raw.round().toString();
  }
}

/// Loads the 4 tile choices, same load-on-init shape as connect_screen's
/// saved host/port: missing/unset entries fall back to
/// [defaultStatusTileKinds].
Future<List<StatusTileKind>> loadStatusTileKinds() async {
  final prefs = await SharedPreferences.getInstance();
  return List.generate(statusTileCount, (i) {
    final index = prefs.getInt('$_prefsKeyPrefix$i');
    if (index == null || index < 0 || index >= StatusTileKind.values.length) {
      return defaultStatusTileKinds[i];
    }
    return StatusTileKind.values[index];
  });
}

/// Saves a single tile's choice, same save-on-change shape as
/// connect_screen's host/port persistence.
Future<void> saveStatusTileKind(int tileIndex, StatusTileKind kind) async {
  final prefs = await SharedPreferences.getInstance();
  await prefs.setInt('$_prefsKeyPrefix$tileIndex', kind.index);
}
