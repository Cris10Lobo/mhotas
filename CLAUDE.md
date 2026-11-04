# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

mHOTAS turns a smartphone into a virtual flight control for Microsoft Flight Simulator 2024: tilt the
phone for pitch/roll, on-screen sliders for throttle/rudder, and live aircraft state (flap position,
gear position) mirrored back to the screen via SimConnect. Unlike prior art (virtualHOTAS,
Fly-By-Wireless, Virtual Yoke Lite), which all inject input through a generic vJoy virtual joystick with
no read path, this project talks to MSFS2024 directly through SimConnect, giving both control fidelity
and a live telemetry channel back to the phone. Input defaults to a vJoy virtual joystick
(`--input=vjoy`, bound in MSFS's own controls menu) or SimConnect events (`--input=simconnect`): two
separate modes, never mixed, with no automatic fallback between them. Either way SimConnect stays
connected and is the source of truth: every readable control state streams back so the phone mirrors
the sim.

Repo is three independent build units tied together by a hand-maintained wire protocol:

- `server/`: Go module `mhotas`, the PC-side server (talks SimConnect, listens for phone UDP packets)
- `app/`: Flutter mobile client (iOS + Android)
- `tools/udp-fake-phone/`: separate Go module, synthetic packet sender for testing the server without a
  phone or MSFS running

Development proceeds in phased milestones (tracked internally, not in this repo). Several backends are
intentionally still stubs; don't assume something is implemented just because it's described here.

## Commands

### Server (`server/`, Go module `mhotas`)

```sh
cd server
go build ./...
go vet ./...
go test ./...
go test ./internal/transport -run TestButtonDeduplicatesByEventID   # single test

go run ./cmd/mhotas-server --backend=log      # dev/test backend, no MSFS needed
go run ./cmd/mhotas-server --backend=simconnect --config path/to/config.json
```

Several packages (`internal/control/simconnect`, `internal/control/vjoy`, `internal/vjoylink`,
`internal/telemetry`'s real source, `internal/simlink`) carry `//go:build windows`, so they have **zero buildable files on
non-Windows**, and are only exercised by cross-compiling:

```sh
GOOS=windows go build ./...
GOOS=windows go vet ./...
```

A plain `go build ./...` / `go vet ./...` / `go test ./...` on Linux/macOS silently skips those packages
rather than failing. Always also run the `GOOS=windows` variants when touching anything under those
paths. Real behavior against a live sim can only be verified on an actual Windows PC with MSFS2024
running; that can't be done from this environment.

To check the sim's side of a change against a live MSFS: the server logs a `sim` line whenever the
sim's control state (flaps, gear, spoilers, throttle, trim, ...) changes, and `make probe VARS='...'`
(`server/cmd/simprobe`) reads any simvar or L:var on demand, e.g. FBW's `L:A32NX_FLAPS_HANDLE_INDEX`.

### udp-fake-phone (`tools/udp-fake-phone/`, separate Go module)

```sh
cd tools/udp-fake-phone
go build ./...
go run . --target=127.0.0.1:9750 --rate=10
```

This module can't import `mhotas/internal/...` (Go's `internal/` visibility rules block cross-module
import regardless of the `replace` directive), which is why the wire protocol package lives at
`server/pkg/protocol` (public) rather than under `internal/`. A `go.work` at the repo root ties this
module and `server/` together for local development, but `go build ./...` from the repo root does **not**
work due to how workspace mode resolves patterns; build from within each module's own directory instead.

End-to-end manual test (validates the whole pipeline without MSFS): run `mhotas-server --backend=log` in
one terminal, `udp-fake-phone` in another, and watch the server's log output, including the failsafe
firing (pitch/roll/rudder snap to neutral, throttle holds) a few hundred ms after the fake phone stops.
Never point the default sweep at a live sim: it toggles button 1, which is gear. `--speedbrake=<0..1>`
holds only the speedbrake, with no buttons and the throttle untouched, and is safe against MSFS.

Against a live sim and a phone on `adb` (wireless debugging works), `app/integration_test/sim_test.dart`
drives the real flying screen and checks each command through TELEMETRY; `survey_test.dart` exercises
every control and logs, for reading alongside `simprobe -watch`. Both take
`--dart-define=MHOTAS_SERVER=<pc-ip>:9750` and need the aircraft on the ground, parking brake set.
`a32nx_clb_test.dart` takes the same define but runs in flight: the A32NX airborne with autopilot and
A/THR on, and the tester changing the FCU speed during its hold.

### Running the live-sim tests

When asked to test against the sim, run the whole flow yourself; only the setup in step 1 needs the tester.

1. **Prompt the tester before running anything**, in one message: load the aircraft under test on the
   ground, parking brake set, sim unpaused; phone on the same LAN with wireless debugging on (give the
   current `ip:port` if `adb connect` fails, since the port changes); vJoy bindings in place (Rx →
   SPOILERS AXIS, buttons as in the server's `vjoy binding` startup lines). For an aircraft without
   spoilers (e.g. Cessna 172), say the spoiler steps will be skipped. Wait for their go-ahead.
2. **Check the PC side** from WSL: `powershell.exe -NoProfile -Command "Get-Process | ? { $_.ProcessName
   -match 'mhotas|FlightSim' }"`. MSFS must be running; if `mhotas-server` isn't, start it yourself.
3. **Start the server** in the background from the repo root, logging to the scratchpad:
   `make server INPUT=vjoy > <scratchpad>/server.log 2>&1` (it cross-builds and runs the Windows exe, so
   it shares the PC's network). Wait for `msg=aircraft` in the log and confirm the title and profile
   match the aircraft under test. To restart, `Stop-Process -Name mhotas-server -Force` first.
4. **Connect the phone**: `adb connect <phone-ip>:<port>`, then `adb devices`.
5. **Run**, from `app/`:
   `flutter test integration_test/sim_test.dart -d <phone-ip>:<port>
   --dart-define=MHOTAS_SERVER=<pc-ip>:9750 [--dart-define=MHOTAS_NO_SPOILERS=true]`, output to a log.
   Each check prints `SIM OK <step>`; a failure prints `sim never reported <step>` with the last
   telemetry.
6. **On failure, read the server log before suspecting the aircraft.** `TimeoutException` at setup
   means no HELLO_ACK (server down or unreachable). A step that never moved: check whether the phone's
   packets reached the server at all (temporarily log in `transport`'s button handler if needed, and
   revert it) and the `sim` lines around that time. A test that pumps no frame between telemetry and
   a drag acts on stale widget state, so that's a test bug, not an app bug.
7. **Report** a pass/fail table per step, what was skipped and why, and anything odd in the server log.
   Leave the server running and tell the tester it is.

### App (`app/`)

`android/` and `ios/` are generated (via `flutter create`) and committed, so a fresh clone builds as-is,
no regeneration step needed. Some dev environments this repo is worked in still lack a Flutter SDK though,
so don't assume the Dart code compiles without checking (CI runs `flutter analyze` on every push).

```sh
cd app
flutter pub get
flutter analyze
flutter test  # headless, see test/control_screen_test.dart below
flutter run   # real device required, motion sensors aren't meaningful on emulators/simulators
```

`test/control_screen_test.dart` is the fast way to check "did I wire this control up right" without a
physical phone: `UdpClient` talks UDP via plain `dart:io`, no platform channel, so `flutter test` can pump
the real `ControlScreen`, tap/drag its real controls, and have the real `UdpClient` send real UDP packets
that the test itself receives and decodes with `protocol.dart`, no real `mhotas-server` process involved
(the Go side's wire→backend mapping already has its own coverage in
`server/internal/transport/server_test.go`). Gyroscope/tilt is the one thing it can't cover: motion
sensors aren't meaningful off real hardware either, so that stays a manual `flutter run` check.

## Architecture

### The wire protocol is the load-bearing contract

`docs/protocol.md` is the source of truth for the UDP binary format between phone and server. It is
**hand-implemented on both sides, deliberately, with no shared codegen**: `server/pkg/protocol/protocol.go`
(Go) and `app/lib/network/protocol.dart` (Dart) must be kept in sync manually. Any packet-layout change
must update the doc and both implementations together. The message set (AXIS, BUTTON, HELLO/HELLO_ACK,
HEARTBEAT, TELEMETRY) is small enough that this was a deliberate choice over adding a codegen toolchain.

Transport is UDP, not TCP/WebSocket, specifically because continuous AXIS data is "latest value wins":
TCP's ordered-delivery guarantee would head-of-line-block subsequent values behind a single dropped
packet, which is worse than just dropping it. See `docs/protocol.md` for the full reasoning, including
the BUTTON redundancy/dedup scheme (events sent 3x, deduped server-side by a monotonic event ID) and the
failsafe policy (axes neutral/hold on a ~500ms AXIS timeout).

### `control.Output` is the seam between transport and simulator

`server/internal/control/output.go` defines the interface (`SetAxis`, `SetButton`, `Close`) that lets
`logbackend` (dev/test, no MSFS needed), `control/simconnect` and `control/vjoy` (both Windows-only)
coexist without `transport` caring which is active. Values reaching a backend are already dead-zone/
curve/invert-shaped by `internal/calibration`; a backend's only job is final range conversion and the
actual write.

`--backend` picks `log` or `simconnect`. `simconnect` always opens the SimConnect connection (telemetry
needs it); `--input` (or the TUI picker when the flag is absent) then picks exactly one write path:
`control/vjoy` or `control/simconnect`, separate backends that never mix (a deliberate choice: an
earlier per-control/whole-server fallback was removed as not worth its complexity). vJoy mode opens a
plain `simlink` connection for telemetry only, and fails at startup if the device lacks any axis or
button it drives. vJoy's mapping is fixed (pitch=Y, roll=X, throttle=Z, rudder=Rz, speedbrake=Rx,
button N = `ButtonID` N) since the MSFS binding is where meaning is set.

Throttle values pass through several spaces, and most bugs on this branch were a transform in the wrong
one: phone lever → `calibration.Curve` (dead zone/expo; *not* invert, see below) → backend
`control.RemapIdle` (`signed_throttle_idle`) → throttle invert (`SetThrottleInvert`: mirrors the final
axis like MSFS's "reverse axis", vJoy only; inverting before the remap swapped forward/reverse around
idle) → axis. The sim's lever reading is in the curve's *output* space: vJoy resync writes it as-is,
and `transport.SendTelemetry` maps it through `Curve.Inverse` before the phone shows it.

A vJoy axis holds its last value, so `control/vjoy` implements `control.ThrottleSyncer`: telemetry
feeds it the sim's throttle and it moves the axis to match after an in-sim change. Never while
autothrottle is driving the lever or the lever is in reverse, and after any write the phone owns the
throttle until the sim's lever stops moving (many aircraft slew it); only a later, finished move away
from that settled position resyncs; see `control/vjoy/sync.go`. The app's `_ThrottleLever` follows the
same settle rule.

### Calibration is server-side except recentering

`internal/calibration.Curve` (dead-zone, expo response curve, invert) is applied server-side, not
phone-side: it's about how the *simulator* responds, not about correcting the phone's physical grip, and
keeping it server-side means one tuning location portable to future client types. The one exception is
the phone's recenter/zero-tilt gesture (`app/lib/sensors/attitude.dart`), which must stay phone-side since
it's instant-feedback correction for how the user is currently holding the device.

### Telemetry is the read-path differentiator

`internal/telemetry` (`Sink` interface with no build tag + a Windows-only `Source` in
`source_windows.go`, backed by `internal/simlink`'s SimConnect data reads) is what makes this project
different from vJoy-based prior art: SimConnect can be *read* as well as written, so flap/gear/throttle/
spoiler/trim position, parking brake, pause, autopilot and autothrottle state all stream back to the
phone as TELEMETRY packets (on change, max ~20Hz, 1Hz keepalive). Telemetry is wired to the SimConnect
connection itself, not to whichever input backend is active. Some aircraft keep their real state
outside the stock simvars, so `config.Aircraft` profiles (matched on `TITLE`) swap the variable a field
is read from (`telemetry/profile.go`, read via `simlink.WatchExtra`); the FBW A32NX ships with one. See `docs/protocol.md`'s "Bidirectional sync" for why several of those exist (closing
the loop so an in-sim change, not just a phone command, reaches the app instead of getting silently
overwritten or misrepresented).

### `internal/simlink` isolates the third-party SimConnect binding

Wraps `github.com/mrlm-net/simconnect`'s actual API so a binding swap (if its axis-SET path ever turns
out unreliable) doesn't ripple through `control/simconnect` and `telemetry`. Confirmed live against a
real MSFS2024 session: `TransmitClientEvent` reliably drives the sim's controls and simvars read back
correctly. Fallback binding candidates if a swap is ever needed: `micmonay/simconnect`,
`JRascagneres/Simconnect-Go`, `grumpypixel/msfs2020-simconnect-go`.

### App structure mirrors the server's data flow

`app/lib/network/` holds the protocol codec and `UdpClient` (raw `dart:io` `RawDatagramSocket`, no
package needed). `app/lib/sensors/attitude.dart` wraps `flutter_rotation_sensor`'s fused orientation
stream (chosen over the alternative `motion_sensors` package, which is unpublished for ~5 years) and
handles the phone-side recenter offset. `app/lib/state/` holds Riverpod providers (`connectionProvider`,
`telemetryProvider`, `sliderAxisProvider` for throttle/rudder; tilt bypasses Riverpod and goes straight
from the sensor stream into the send loop, since it updates far more often than widget state should).
`app/lib/ui/` has four screens: `connect_screen.dart` (IP:port entry, or `scan_screen.dart` to read the
QR code the server's TUI shows), `control_screen.dart` (the X-Plane-Mobile-style flying screen), and
`settings_screen.dart` (app toggles plus per-axis calibration, sent as CONFIG_SET and persisted to the
server's config file).

### Every draggable control on the flying screen must go through `_Movable`

`control_screen.dart`'s free-position layout (drag a control anywhere, offset persisted) wraps each
draggable widget in `_Movable`. Positioning a widget any other way (a bare `Transform.translate` by a
saved offset, most obviously) silently reintroduces a real, already-hit-twice bug: `Transform.translate`
moves where a widget *paints* but not its *layout slot*, and Flex (Row/Column) hit-testing is keyed to
that untouched slot. Once a moved widget's new position overlaps a different, unmoved sibling's original
footprint, that sibling's own surface, still sitting exactly at its own slot, steals every future touch
there, and the moved widget's own taps silently do nothing (or trigger whatever it's now covering)
instead. First hit as "some dragged controls become unreachable" (Phase 6), then again as a brand-new
control's buttons doing nothing once dragged and locked (Phase 10, the trim rocker), because the first
fix only kept the Overlay active *while actively dragging*, not once the layout was locked back with a
nonzero offset still in effect. `_Movable` now keeps the Overlay-based, actually-correctly-hit-tested
positioning active for *any* nonzero saved offset, not just mid-drag, so every current and future control
that goes through it is covered automatically; see `_Movable`'s own doc comment in `control_screen.dart`
for the mechanism. Do not hand-roll positioning for a new control on this screen.

## Distribution

Open-source (MIT), not published to app stores; the app is meant to be sideloaded. Bundle ID is
`com.mhotas.app` on both platforms. Pushing a `v*` tag runs `.github/workflows/release.yml`, which
attaches the Android APK and a Windows server zip (exe, `SimConnect.dll`, default `config.json`,
`start-mhotas.bat`) to a GitHub release. `SimConnect.dll` is deliberately not in git: the MSFS SDK EULA
doesn't clearly allow redistributing it, so it ships only as a release asset (as Little Navmap does),
easy to pull if Microsoft objects. The workflow probes Microsoft's public, login-free installer URL
(`sdk.flightsimulator.com/msfs2024/files/installers/<v>/...`) upward from `SDK_FLOOR` for the newest
version, since there's no "latest" link, then extracts the DLL with `msiextract`. Bump `SDK_FLOOR` if
the SDK ever moves past `floor + 3` minor versions or to a new major. `simlink`
loads the copy next to the exe unless `SIMCONNECT_DLL` is set, else falls back to SDK auto-detection. The release APK is signed with the CI runner's debug key, so each release has a
different signature until a real keystore is set up. The main `AndroidManifest.xml` must keep the
`INTERNET` permission: Flutter only adds it to debug/profile builds, and without it the release app
can't open its UDP socket.
