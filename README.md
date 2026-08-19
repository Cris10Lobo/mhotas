# mHOTAS — Virtual Mobile HOTAS for MSFS2024

[![Go](https://github.com/Cris10Lobo/mhotas/actions/workflows/go.yml/badge.svg)](https://github.com/Cris10Lobo/mhotas/actions/workflows/go.yml)
[![Flutter](https://github.com/Cris10Lobo/mhotas/actions/workflows/flutter.yml/badge.svg)](https://github.com/Cris10Lobo/mhotas/actions/workflows/flutter.yml)

Turn a smartphone into a virtual flight control for Microsoft Flight Simulator 2024: tilt the phone
like a stick for pitch/roll, use on-screen sliders for throttle/rudder, and see live aircraft state
(flap position, gear position) mirrored back to the screen in real time.

Unlike existing phone-as-joystick tools (which all inject input via a generic virtual joystick driver
like vJoy, with no way to read anything back), mHOTAS talks to MSFS2024 directly via
[SimConnect](https://docs.flightsimulator.com/msfs2024/html/6_Programming_APIs/SimConnect/SimConnect_API_Reference.htm),
giving it two advantages:

- **Control fidelity**: control axes are set via SimConnect's own client events, not a virtual HID device.
- **Live telemetry**: SimConnect can also be *read from* — mHOTAS streams live simvars (starting with
  flap handle index and gear position) back to the phone, so the app's on-screen indicators track the
  simulator in real time.

A vJoy-based backend is planned as a later addition, for broader sim compatibility beyond MSFS2024 and
as a fallback if direct SimConnect axis control proves unreliable for some aircraft.

## Status

Early development. See [docs/protocol.md](docs/protocol.md) for the wire protocol between the phone app
and the PC server.

## Repo layout

- `server/` — Go PC-side server (module `mhotas`). Talks SimConnect, listens for UDP control packets
  from the phone, drives the flight sim.
- `app/` — Flutter mobile client (iOS + Android). Streams tilt/throttle/rudder/buttons to the server,
  displays live telemetry.
- `tools/udp-fake-phone/` — synthetic packet sender for testing the server without a phone or MSFS running.
- `docs/` — wire protocol spec.

## Requirements

- **Server**: Go 1.25+, Windows (SimConnect/MSFS2024 requirement), MSFS2024 installed for real
  end-to-end testing. The server itself cross-compiles from any OS (`GOOS=windows go build ./...`), but
  SimConnect calls only function on Windows with MSFS2024 running.
- **App**: Flutter SDK, a physical iOS or Android device (motion sensors aren't meaningful on emulators).

## Development

```sh
# Server (from server/), no MSFS needed — validates the pipeline with the log backend:
cd server
go run ./cmd/mhotas-server --backend=log

# In another terminal, send synthetic phone packets:
cd tools/udp-fake-phone
go run . --target=127.0.0.1:9750
```

### App

```sh
cd app
flutter pub get
flutter analyze
flutter run   # on a real device — motion sensors aren't meaningful on emulators/simulators
```
