# mHOTAS Wire Protocol

This is the source of truth for the UDP protocol between the Flutter phone app and the Go PC server.
Both sides hand-implement against this document — there is no shared codegen. If you change a packet
layout, update this file in the same change.

- Transport: **UDP**, single socket, both directions (bidirectional once the phone's address is known
  to the server via `HELLO`).
- Default port: **9750**.
- Byte order: **little-endian** throughout.
- All multi-byte numeric fields are fixed-width; there is no variable-length encoding in v1.

## Header (all packets)

Every packet starts with a 4-byte header:

| Offset | Size | Field           | Notes                                              |
|--------|------|-----------------|-----------------------------------------------------|
| 0      | 1    | `type`          | See packet type table below                        |
| 1      | 1    | `version`       | Protocol version, currently `8`                     |
| 2      | 2    | `seq`           | uint16, per-packet-type sequence number, wraps at 65535 |

## Packet types

| Type | Name         | Direction        | Rate                          |
|------|--------------|------------------|--------------------------------|
| 0x01 | `AXIS`       | phone → server   | 30-50Hz, continuous            |
| 0x02 | `BUTTON`     | phone → server   | on press/release, 3x redundant over ~150ms |
| 0x03 | `HELLO`      | phone → server   | once, on connect               |
| 0x04 | `HELLO_ACK`  | server → phone   | once, in reply to `HELLO`      |
| 0x05 | `HEARTBEAT`  | both directions  | ~1Hz                            |
| 0x06 | `TELEMETRY`  | server → phone   | on change (max ~20Hz), else 1Hz |
| 0x07 | `CONFIG`     | server → phone   | after HELLO_ACK, after CONFIG_SET, in reply to CONFIG_GET |
| 0x08 | `CONFIG_SET` | phone → server   | on a calibration edit           |
| 0x09 | `CONFIG_GET` | phone → server   | on settings screen open         |

### `AXIS` (0x01) — 20 bytes total

| Offset | Size | Field      | Range          |
|--------|------|------------|----------------|
| 4      | 4    | `pitch`    | float32, -1..1 |
| 8      | 4    | `roll`     | float32, -1..1 |
| 12     | 4    | `throttle` | float32, -1..1 (negative = reverse thrust) |
| 16     | 4    | `rudder`   | float32, -1..1 |

No dead-zone or curve is applied on the phone — it sends raw normalized tilt/slider position. The
server applies dead-zone, expo curve, invert, and range conversion for the active `ControlOutput`
backend (see `server/internal/calibration` and `server/internal/control`). The phone *does* apply the
recenter/zero offset before normalizing, since that's correcting for physical grip, not sim response.

### `BUTTON` (0x02) — 8 bytes total

| Offset | Size | Field      | Notes                                                |
|--------|------|------------|-------------------------------------------------------|
| 4      | 1    | `buttonId` | app-defined, see button map in server config          |
| 5      | 1    | `pressed`  | 0 = released, 1 = pressed                              |
| 6      | 2    | `eventId`  | uint16, monotonic per button, wraps                    |

Sent 3x in immediate succession (no fixed inter-packet delay requirement, just "close together") so a
single dropped UDP packet doesn't lose a discrete press. The server de-dupes: for a given `buttonId`, a
packet is applied only if its `eventId` is newer (mod-wraparound-aware) than the last applied `eventId`
for that button; older/duplicate/out-of-order packets are discarded without effect.

### `HELLO` (0x03) — 36 bytes total

| Offset | Size | Field        | Notes                                  |
|--------|------|--------------|------------------------------------------|
| 4      | 32   | `deviceName` | UTF-8, NUL-padded, truncated at 31 bytes |

### `HELLO_ACK` (0x04) — 37 bytes total

| Offset | Size | Field        | Notes                                                |
|--------|------|--------------|--------------------------------------------------------|
| 4      | 1    | `status`     | 0 = ok, 1 = version mismatch (see below)               |
| 5      | 32   | `serverName` | UTF-8, NUL-padded, truncated at 31 bytes               |

If `version` in the `HELLO` header doesn't match the server's supported version, the server still
replies with `HELLO_ACK{status=1}` (not silence) so the app can show a clear "incompatible server
version" error instead of a generic connection failure.

### `HEARTBEAT` (0x05) — 4 bytes total (header only)

No payload. Either side sends this roughly once per second while a session is active. Used purely for
liveness/UI state ("connected" vs "connection lost") and to drive the server's failsafe timer — it is
not required for `AXIS`/`BUTTON` processing to function.

### `TELEMETRY` (0x06) — 26 bytes total

| Offset | Size | Field                 | Notes                                                    |
|--------|------|------------------------|-------------------------------------------------------------|
| 4      | 1    | `flapHandleIndex`      | int8, current flap handle position                          |
| 5      | 1    | `flapHandleIndexMax`   | int8, max flap handle index for the loaded aircraft         |
| 6      | 1    | `gearPosition`         | uint8: 0 = up, 1 = down, 2 = in transit                     |
| 7      | 1    | `parkingBrakeSet`      | uint8: 0/1, the sim's *actual* current parking brake state, independent of the phone's own button-tap history |
| 8      | 4    | `throttlePercent`      | float32, -1..1, the sim's *actual* current throttle lever position (negative = reverse), independent of whatever the phone last commanded |
| 12     | 1    | `paused`               | uint8: 0/1, the sim's *actual* current pause state, from SimConnect's own Pause system event, not a guess based on the phone's last PAUSE tap |
| 13     | 1    | `flags`                | bitmask: bit0 = `stallWarning` (SimConnect `STALL WARNING`), bit1 = `onGround` (`SIM ON GROUND`), bit2 = `spoilersArmed` (`SPOILERS ARMED`), bit3 = `autopilotMaster` (`AUTOPILOT MASTER`), bit4 = `autothrottleActive` (`AUTOPILOT THROTTLE ARM` or `AUTOPILOT MANAGED THROTTLE ACTIVE`), bits 5-7 unused. Was a plain "reserved, must be 0" byte through v6. |
| 14     | 4    | `groundSpeedKnots`     | float32, `GROUND VELOCITY` in knots — drives the taxi/ground-roll rumble haptic's pulse rate; meaningless while `onGround` is false |
| 18     | 4    | `spoilersHandle`       | float32, 0..1, `SPOILERS HANDLE POSITION` — the speedbrake lever mirrors it |
| 22     | 4    | `elevatorTrim`         | float32, -1..1, `ELEVATOR TRIM PCT` — shown on the trim rocker |

These fields exist so the app can mirror the sim's real state rather than silently fighting a manual
in-sim adjustment or guessing state locally — see "Bidirectional sync" below. A fixed struct rather than
a generic key/value simvar schema. (v2-v5 carried 4 extra fields — airspeed/altitude/heading/vertical
speed — for on-screen status tiles; removed in v6 along with the status tiles themselves. v7 adds
`flags`/`groundSpeedKnots` for haptic feedback — stall-warning stick-shaker and taxi rumble. v8 adds
`spoilersHandle`/`elevatorTrim` and flag bits 2-4 so every control with readable sim state mirrors it.)

The server sends a packet whenever any field changed, at most every 50ms (SimConnect reports every
changed sim frame), and re-sends unchanged state once a second so a phone that just (re)connected gets
the full picture without waiting for something in the sim to change.

### `CONFIG` (0x07) — 76 bytes total

Full snapshot of the server's current dead-zone/expo/invert/smoothing/signed-throttle for every axis,
same "whole struct, not a diff" approach as `TELEMETRY`. Sent unprompted right after `HELLO_ACK`, again
after every applied `CONFIG_SET`, and in reply to a `CONFIG_GET`. Failsafe policy isn't part of this — it
stays a server-config-file-only setting, not phone-editable.

Four fixed-order 18-byte axis-calibration blocks, pitch/roll/throttle/rudder:

| Offset (within block) | Size | Field                | Range          |
|------------------------|------|----------------------|----------------|
| 0                       | 4    | `deadZone`           | float32, 0..1  |
| 4                       | 4    | `expo`               | float32, 0..1  |
| 8                       | 1    | `invert`             | uint8: 0/1     |
| 9                       | 4    | `smoothing`          | float32, 0..1  |
| 13                      | 1    | `signedThrottle`     | uint8: 0/1     |
| 14                      | 4    | `signedThrottleIdle` | float32, -1..1 |

On the `throttle` block, `invert` reverses the final axis output after the idle remap (like MSFS's own
"reverse axis"), not the input value: inverting the input would swap the forward and reverse zones around
`signedThrottleIdle`, putting phone idle far up the forward range whenever idle isn't at the center.

`signedThrottle`/`signedThrottleIdle` only mean anything on the `throttle` block — see "Reverse-thrust
convention" below. Carried on every axis anyway, to keep one shared block layout; a no-op on
pitch/roll/rudder.

| Offset | Size | Field      |
|--------|------|------------|
| 4      | 18   | `pitch`    |
| 22     | 18   | `roll`     |
| 40     | 18   | `throttle` |
| 58     | 18   | `rudder`   |

### `CONFIG_SET` (0x08) — 23 bytes total

| Offset | Size | Field         | Notes                                             |
|--------|------|---------------|----------------------------------------------------|
| 4      | 1    | `axis`        | 0=pitch, 1=roll, 2=throttle, 3=rudder               |
| 5      | 18   | `calibration` | same axis-calibration block layout as `CONFIG`      |

Changes one axis's dead-zone/expo/invert/smoothing/signed-throttle. The server clamps
`deadZone`/`expo`/`smoothing` to `[0,1]` and `signedThrottleIdle` to `[-1,1]` (a malformed or
stale-client value shouldn't be able to put the calibration curve, or the signed-throttle remap, into
undefined/NaN/out-of-range territory), preserves that axis's existing failsafe policy untouched, persists
the full config back to its config file (if one was given via `-config`; a no-config-file run just
applies the change for the session), and replies with a fresh `CONFIG` — there's no separate ack packet
type.

#### Reverse-thrust convention (`signedThrottle`/`signedThrottleIdle`, throttle axis only)

SimConnect's `THROTTLE_AXIS_SET_EX1` has two known reverse-thrust conventions, and which one a given
aircraft actually wants isn't discoverable — it depends on that aircraft's own throttle-calibration
system, not anything mHOTAS can detect. `signedThrottle = false` (default) arms/disarms reverse via a
toggle and sends an always-positive magnitude either way. `signedThrottle = true` sends the calibrated
`-1..1` value straight through as one continuous signed value, with no arm step — some aircraft's own
calibration systems expect this instead. `signedThrottleIdle` is that signed value's own idle point
(`0`, the default, means no remap — the raw signed value passes straight through); where "idle" actually
falls on a given aircraft's own signed scale is specific to that aircraft, so this is a plain tunable
number, not a value mHOTAS assumes. The phone's Settings screen exposes both as controls on the Throttle
calibration card, sent like any other calibration edit.

### `CONFIG_GET` (0x09) — 4 bytes total (header only)

Requests a `CONFIG` reply. Unlike `TELEMETRY`'s recurring stream, `CONFIG` is only ever sent once
unprompted (right after `HELLO_ACK`) or on an edit — a settings screen that wasn't mounted yet when that
one-shot arrived has nothing to fall back on without explicitly asking again, so the app sends this
whenever the settings screen opens.

## Bidirectional sync

Two separate mechanisms close the "double lane" gap where an in-sim change (mouse, keyboard,
autothrottle, another control, the Esc key) previously had no way to reach the phone, or got silently
overwritten by it:

- **Axes stop being re-asserted once unchanged.** `AXIS` is phone → server only; the server never treats
  "no change since last packet" as a reason to stop reading, but it *does* stop re-asserting a value to
  the backend once it's unchanged (see `transport.Server.applyAxis`/`smooth`) — otherwise a continuous
  30-50Hz resend of the same throttle/rudder value would immediately overwrite any adjustment made
  directly in the sim. This applies uniformly to every axis (pitch/roll included — it's just a no-op
  there in practice, since sensor noise means tilt essentially never repeats bit-for-bit), so it's the
  one thing that "does this for everything" rather than throttle specifically.
- **`TELEMETRY` reads state back for widgets/buttons that would otherwise assume they're the only writer.**
  `throttlePercent` lets the throttle lever mirror the sim's real position while not being touched, so
  the pilot sees the truth and the next drag starts from there instead of jumping from a stale value.
  `parkingBrakeSet`/`paused` do the analogous thing for buttons — a toggle button can't "fight" the sim
  the way a continuously-resent axis can (each tap just flips whatever the current state is, from
  either side), but without a readback the app has no way to *show* the real state, so `_BigButton`
  lights up from these fields instead of a locally-tracked guess.

Not every control gets a `TELEMETRY` readback: pitch/roll/rudder have no on-screen "position" widget to
mirror (tilt is physical, and the rudder stick is deliberately momentary — it always springs back to a
fixed neutral, so there's no stale rest value to correct), so the axis dedup above is their complete
treatment. Every other control mirrors its sim state: flaps, gear, parking brake, pause, throttle,
spoiler handle and arm, elevator trim, autopilot master, and autothrottle (shown as an A/T badge on the
throttle lever). Adding another follows the same pattern: read the simvar, add a `TELEMETRY` field,
mirror it client-side.

## Failsafe policy (server-side)

If the server stops receiving `AXIS` packets for **500ms** (configurable), it applies:

| Axis     | Failsafe behavior |
|----------|--------------------|
| pitch    | snap to 0 (neutral) |
| roll     | snap to 0 (neutral) |
| rudder   | snap to 0 (neutral) |
| throttle | hold last value      |

Buttons are stateless events, not held — no failsafe action needed for them. This default was chosen
because an unattended tilt input is a "hands off" signal and should neutralize, while an unexpected
throttle cut is arguably a worse mid-flight surprise than holding position. It's a per-axis config
value (`server/internal/config`), not hardcoded, since it's a judgment call the operator may want to
override.

## Discovery / connection (v1: manual only)

v1 uses **manual IP:port entry** in the app (remembered locally). UDP broadcast discovery
(`VHOTAS_DISCOVER` / `VHOTAS_HERE:<name>:<port>`) is a documented Phase 2 addition, not part of this
version of the protocol.
