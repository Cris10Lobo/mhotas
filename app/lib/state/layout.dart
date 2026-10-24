import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _prefsKey = 'mhotas.layout.offsets';

/// Per-control drag offset from its flex-computed position, keyed by widget id.
class LayoutOffsetsNotifier extends StateNotifier<Map<String, Offset>> {
  LayoutOffsetsNotifier() : super(const {}) {
    _load();
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(_prefsKey);
    if (raw == null) return;
    final decoded = jsonDecode(raw) as Map<String, dynamic>;
    state = {
      for (final entry in decoded.entries)
        entry.key: Offset(
          (entry.value as List)[0] as double,
          (entry.value)[1] as double,
        ),
    };
  }

  void addDelta(String id, Offset delta) {
    final current = state[id] ?? Offset.zero;
    state = {...state, id: current + delta};
    _save();
  }

  Future<void> reset() async {
    state = const {};
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_prefsKey);
  }

  Future<void> _save() async {
    final prefs = await SharedPreferences.getInstance();
    final encoded = state.map((id, o) => MapEntry(id, [o.dx, o.dy]));
    await prefs.setString(_prefsKey, jsonEncode(encoded));
  }
}

final layoutOffsetsProvider =
    StateNotifierProvider<LayoutOffsetsNotifier, Map<String, Offset>>(
  (ref) => LayoutOffsetsNotifier(),
);

/// Not persisted, so a fresh launch always starts locked.
final layoutEditModeProvider = StateProvider<bool>((ref) => false);
