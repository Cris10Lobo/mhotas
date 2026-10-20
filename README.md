# mHOTAS — Virtual Mobile HOTAS for MSFS2024

[![Go](https://github.com/Cris10Lobo/mhotas/actions/workflows/go.yml/badge.svg)](https://github.com/Cris10Lobo/mhotas/actions/workflows/go.yml)
[![Flutter](https://github.com/Cris10Lobo/mhotas/actions/workflows/flutter.yml/badge.svg)](https://github.com/Cris10Lobo/mhotas/actions/workflows/flutter.yml)

Turn a smartphone into a virtual flight control for Microsoft Flight Simulator 2024: tilt the phone
like a stick for pitch/roll, use on-screen sliders for throttle/rudder, and see live aircraft state
(flap position, gear position) mirrored back to the screen in real time.

Existing phone-as-joystick tools inject input through a virtual joystick driver like vJoy, with no way to
read anything back. mHOTAS also uses vJoy for input by default, but stays connected to MSFS2024 via
[SimConnect](https://docs.flightsimulator.com/msfs2024/html/6_Programming_APIs/SimConnect/SimConnect_API_Reference.htm),
which gives it two advantages:

- **Two input paths**: vJoy (default) works through any aircraft's standard MSFS bindings; SimConnect's
  own client events are the alternative. You pick one; they never mix.
- **Live telemetry**: SimConnect can also be *read from* — mHOTAS streams live simvars (flaps, gear,
  throttle, spoilers, trim, brakes, pause, autopilot, autothrottle) back to the phone, so the app's on-screen indicators track the
  simulator in real time.

## Input: vJoy (default) or SimConnect

`--backend=simconnect` sends input through a [vJoy](https://github.com/BrunnerInnovation/vJoy) virtual
joystick by default (`--input=vjoy`), while SimConnect stays connected to read the sim's state back —
the sim is the source of truth, and the phone mirrors it. Without the `--input` flag the server's TUI
asks at startup (Enter keeps vJoy). The two modes are separate: in vJoy mode every control goes through
vJoy and SimConnect only reads telemetry, and if vJoy isn't installed or the device lacks an axis or
button, the server stops with what to fix — it never quietly switches to SimConnect. `--input=simconnect`
uses SimConnect events for everything instead.

vJoy setup:

1. Install vJoy, open **Configure vJoy**, and on device 1 enable axes **X, Y, Z, Rz** and **15 buttons**.
2. In MSFS → Controls → vJoy Device, bind:

   | vJoy | Control | MSFS binding |
   |------|---------|--------------|
   | Y | pitch | Elevator axis |
   | X | roll | Ailerons axis |
   | Z | throttle | Throttle axis (`-100 to 100%` if `signed_throttle` is on, else `0 to 100%`) |
   | Rz | rudder | Rudder axis |
   | 1 | gear | Toggle landing gear |
   | 2 | parking_brake | Toggle parking brake |
   | 3 | pause | Toggle pause |
   | 4 / 5 | flap_incr / flap_decr | Increase / decrease flaps |
   | 6 | toe_brake | Brakes (hold) |
   | 7 / 8 | spoiler_incr / spoiler_decr | Increase / decrease spoilers |
   | 9 | spoiler_arm | Toggle arm spoilers |
   | 10 | autopilot_disconnect | Autopilot off |
   | 11 / 12 | trim_up / trim_down | Elevator trim up / down |
   | 13 / 14 | throttle_incr / throttle_decr | Increase / decrease throttle |
   | 15 | throttle_reverse_arm | Reverse thrust (hold) |

   With `signed_throttle` on, `signed_throttle_idle` (Settings → Throttle on the phone, applied live)
   sets where idle sits on the vJoy axis — on a detented airliner it's well below the middle. Tune it
   until the phone's idle puts the sim's lever exactly at idle.

   The server also logs this table at startup. Pitch direction comes from the MSFS binding: if it's
   backwards, tick "reverse axis" there. Throttle invert (Settings → Throttle) does the same job for the
   throttle and only applies in vJoy mode; SimConnect mode ignores it.

While the sim's autothrottle is active, the throttle lever shows an **A/T** badge and the server stops
moving the vJoy throttle to follow the sim, so it can't knock autothrottle off.

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
