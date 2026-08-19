# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

mHOTAS turns a smartphone into a virtual flight control for Microsoft Flight Simulator 2024: tilt the
phone for pitch/roll, on-screen sliders for throttle/rudder, and live aircraft state (flap position,
gear position) mirrored back to the screen via SimConnect. Unlike prior art (virtualHOTAS,
Fly-By-Wireless, Virtual Yoke Lite), which all inject input through a generic vJoy virtual joystick with
no read path, this project talks to MSFS2024 directly through SimConnect — giving both control fidelity
and a live telemetry channel back to the phone. A vJoy backend is planned later for broader sim
compatibility and as a fallback if direct SimConnect control proves unreliable.

Repo is three independent build units tied together by a hand-maintained wire protocol:

- `server/` — Go module `mhotas`, the PC-side server (talks SimConnect, listens for phone UDP packets)
- `app/` — Flutter mobile client (iOS + Android)
- `tools/udp-fake-phone/` — separate Go module, synthetic packet sender for testing the server without a
  phone or MSFS running

Development proceeds in phased milestones (tracked internally, not in this repo) — several backends are
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

Several packages (`internal/control/simconnect`, `internal/control/vjoy`, `internal/telemetry`'s real
source, `internal/simlink`, `cmd/spike-simconnect`) carry `//go:build windows` — they have **zero
buildable files on non-Windows**, and are only exercised by cross-compiling:

```sh
GOOS=windows go build ./...
GOOS=windows go vet ./...
```

A plain `go build ./...` / `go vet ./...` / `go test ./...` on Linux/macOS silently skips those packages
rather than failing — always also run the `GOOS=windows` variants when touching anything under those
paths. Real behavior against a live sim can only be verified on an actual Windows PC with MSFS2024
running; that can't be done from this environment.

### udp-fake-phone (`tools/udp-fake-phone/`, separate Go module)

```sh
cd tools/udp-fake-phone
go build ./...
go run . --target=127.0.0.1:9750 --rate=10
```

This module can't import `mhotas/internal/...` (Go's `internal/` visibility rules block cross-module
import regardless of the `replace` directive), which is why the wire protocol package lives at
`server/pkg/protocol` (public) rather than under `internal/`. A `go.work` at the repo root ties this
module and `server/` together for local development — `go build ./...` from the repo root does **not**
work due to how workspace mode resolves patterns; build from within each module's own directory instead.

End-to-end manual test (validates the whole pipeline without MSFS): run `mhotas-server --backend=log` in
one terminal, `udp-fake-phone` in another, and watch the server's log output — including the failsafe
firing (pitch/roll/rudder snap to neutral, throttle holds) a few hundred ms after the fake phone stops.

### App (`app/`)

No Flutter SDK is available in some dev environments this repo is worked in — `app/lib/` and
`pubspec.yaml` may be ahead of what's been run through `flutter analyze`; don't assume the Dart code
compiles without checking. To get a working project from scratch:

```sh
cd app
flutter create --org <your.org> --project-name mhotas .   # generates android/, ios/
git checkout -- pubspec.yaml lib/                          # flutter create overwrites both
flutter pub get
flutter analyze
flutter run   # real device required — motion sensors aren't meaningful on emulators/simulators
```

## Architecture

### The wire protocol is the load-bearing contract

`docs/protocol.md` is the source of truth for the UDP binary format between phone and server. It is
**hand-implemented on both sides, deliberately, with no shared codegen** — `server/pkg/protocol/protocol.go`
(Go) and `app/lib/network/protocol.dart` (Dart) must be kept in sync manually. Any packet-layout change
must update the doc and both implementations together. The message set (AXIS, BUTTON, HELLO/HELLO_ACK,
HEARTBEAT, TELEMETRY) is small enough that this was a deliberate choice over adding a codegen toolchain.

Transport is UDP, not TCP/WebSocket, specifically because continuous AXIS data is "latest value wins" —
TCP's ordered-delivery guarantee would head-of-line-block subsequent values behind a single dropped
packet, which is worse than just dropping it. See `docs/protocol.md` for the full reasoning, including
the BUTTON redundancy/dedup scheme (events sent 3x, deduped server-side by a monotonic event ID) and the
failsafe policy (axes neutral/hold on a ~500ms AXIS timeout).

### `control.Output` is the seam between transport and simulator

`server/internal/control/output.go` defines the interface (`SetAxis`, `SetButton`, `Close`) that lets
`logbackend` (dev/test, no MSFS needed), `control/simconnect` (MVP, Windows-only), and `control/vjoy`
(a later-phase addition, still stubbed) coexist without `transport` caring which is active.
`cmd/mhotas-server`'s `--backend` flag selects one. Values
reaching a backend are already dead-zone/curve/invert-shaped by `internal/calibration` — a backend's only
job is final range conversion and the actual write.

### Calibration is server-side except recentering

`internal/calibration.Curve` (dead-zone, expo response curve, invert) is applied server-side, not
phone-side — it's about how the *simulator* responds, not about correcting the phone's physical grip, and
keeping it server-side means one tuning location portable to future client types. The one exception is
the phone's recenter/zero-tilt gesture (`app/lib/sensors/attitude.dart`), which must stay phone-side since
it's instant-feedback correction for how the user is currently holding the device.

### Telemetry is the read-path differentiator

`internal/telemetry` (`Sink` interface with no build tag + a Windows-only `Source` in
`source_windows.go`, backed by `internal/simlink`'s SimConnect data reads) is what makes this project
different from vJoy-based prior art: SimConnect can be *read* as well as written, so flap position, gear
position, throttle lever position, parking brake state, and pause state all stream back to the phone as
TELEMETRY packets — see `docs/protocol.md`'s "Bidirectional sync" for why several of those exist (closing
the loop so an in-sim change, not just a phone command, reaches the app instead of getting silently
overwritten or misrepresented).

### `internal/simlink` isolates the third-party SimConnect binding

Intended to wrap `github.com/mrlm-net/simconnect`'s actual API so a binding swap — if the SimConnect
spike finds its axis-SET path unreliable — doesn't ripple through `control/simconnect` and `telemetry`.
Fallback binding candidates if a swap is ever needed: `micmonay/simconnect`, `JRascagneres/Simconnect-Go`,
`grumpypixel/msfs2020-simconnect-go`.

### The SimConnect spike gated everything SimConnect-related

`cmd/spike-simconnect` is a throwaway program that answered one question before anything else got built
on top: can `mrlm-net/simconnect` reliably drive MSFS2024's controls (`TransmitClientEvent`) and read live
simvars back. Confirmed live against a real MSFS2024 session — see that command's own doc comments for
what was validated.

### App structure mirrors the server's data flow

`app/lib/network/` holds the protocol codec and `UdpClient` (raw `dart:io` `RawDatagramSocket`, no
package needed). `app/lib/sensors/attitude.dart` wraps `flutter_rotation_sensor`'s fused orientation
stream (chosen over the alternative `motion_sensors` package, which is unpublished for ~5 years) and
handles the phone-side recenter offset. `app/lib/state/` holds Riverpod providers (`connectionProvider`,
`telemetryProvider`, `sliderAxisProvider` for throttle/rudder — tilt bypasses Riverpod and goes straight
from the sensor stream into the send loop, since it updates far more often than widget state should).
`app/lib/ui/` has three screens: `connect_screen.dart` (manual IP:port entry, v1 has no auto-discovery),
`control_screen.dart` (the X-Plane-Mobile-style flying screen), `settings_screen.dart` (currently a
placeholder — calibration tuning lives in the server's config file until a later phase).

## Distribution

Open-source (MIT), not published to app stores — the app is meant to be sideloaded.
