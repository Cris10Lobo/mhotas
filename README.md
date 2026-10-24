# mHOTAS

[![Go](https://github.com/Cris10Lobo/mhotas/actions/workflows/go.yml/badge.svg)](https://github.com/Cris10Lobo/mhotas/actions/workflows/go.yml)
[![Flutter](https://github.com/Cris10Lobo/mhotas/actions/workflows/flutter.yml/badge.svg)](https://github.com/Cris10Lobo/mhotas/actions/workflows/flutter.yml)

mHOTAS turns your phone into a flight control for Microsoft Flight Simulator 2024. Tilt the phone to
fly pitch and roll, use the on-screen levers for throttle and rudder, and tap buttons for gear, flaps,
spoilers, trim and brakes. The phone also shows what the aircraft is doing, so if you move the flaps
with your mouse in the sim, the app's flap indicator follows.

Most phone joystick apps push input through a virtual joystick driver and can't read anything back.
mHOTAS also uses a virtual joystick (vJoy) for input by default, but it stays connected to the sim
through [SimConnect](https://docs.flightsimulator.com/msfs2024/html/6_Programming_APIs/SimConnect/SimConnect_API_Reference.htm)
and streams flaps, gear, throttle, spoilers, trim, parking brake, pause, autopilot and autothrottle
state back to the phone.

This is an alpha. Expect rough edges, and please open an issue when something breaks.

## What you need

- A Windows PC running MSFS 2024.
- An Android phone, or an iPhone plus a Mac with Xcode (see below).
- The phone and the PC on the same Wi-Fi network.
- [vJoy](https://github.com/BrunnerInnovation/vJoy/releases) installed on the PC (for the default input mode).

## Install

### 1. Download

Grab the latest files from the [Releases page](https://github.com/Cris10Lobo/mhotas/releases):

- `mhotas-<version>.apk` for your Android phone
- `mhotas-server-<version>-windows.zip` for your PC

### 2. Install the app on Android

1. Copy the APK to your phone, or download it straight from the Releases page on the phone.
2. Open it. Android will ask you to allow installs from that source (your browser or file manager). Allow it.
3. Tap Install.

Alpha builds aren't signed with a stable key yet, so to update you may need to uninstall the old
version first.

### 2b. Install the app on iPhone

There's no iOS download, because Apple doesn't allow sideloading an app file the way Android does. You
can build it yourself on a Mac:

1. Install [Flutter](https://docs.flutter.dev/get-started/install) and Xcode.
2. Clone this repo, then run `cd app && flutter pub get`.
3. Open `app/ios/Runner.xcworkspace` in Xcode, pick your Apple ID under Signing & Capabilities, and
   change the bundle identifier to something unique to you.
4. Plug in your iPhone and run `flutter run --release`.

With a free Apple ID the app stops launching after 7 days and you'll have to run it again from Xcode.

### 3. Set up vJoy

1. Install vJoy and open Configure vJoy.
2. On device 1, enable the axes X, Y, Z and Rz, set the number of buttons to 15, and click Apply.

### 4. Start the server

1. Unzip `mhotas-server-<version>-windows.zip` anywhere you like.
2. Start MSFS 2024 and load into a flight.
3. Double-click `start-mhotas.bat`. The first time, Windows Firewall will ask whether to allow
   mhotas-server on the network. Allow it on private networks, or the phone can't reach it.
4. The server asks which input mode to use. Press Enter for vJoy.

The server window shows a QR code and the address it's listening on (port 9750 by default).

### 5. Connect the phone

Open mHOTAS on the phone and tap Scan QR code, then point the camera at the QR code in the server
window. If scanning doesn't work, type the IP address and port shown under the QR code instead.

Hold the phone the way you want to fly and tap Calibrate at the top of the screen to set that as neutral.

### 6. Bind the controls in MSFS

In MSFS, go to Controls, select vJoy Device, and bind each input. The server also prints this table
when it starts.

| vJoy | Control | MSFS binding |
|------|---------|--------------|
| Y | pitch | Elevator axis |
| X | roll | Ailerons axis |
| Z | throttle | Throttle axis (`-100 to 100%` if signed reverse thrust is on, otherwise `0 to 100%`) |
| Rz | rudder | Rudder axis |
| 1 | gear | Toggle landing gear |
| 2 | parking brake | Toggle parking brake |
| 3 | pause | Toggle pause |
| 4 / 5 | flaps up / down | Increase / decrease flaps |
| 6 | toe brake | Brakes (hold) |
| 7 / 8 | spoilers up / down | Increase / decrease spoilers |
| 9 | spoiler arm | Toggle arm spoilers |
| 10 | autopilot disconnect | Autopilot off |
| 11 / 12 | trim up / down | Elevator trim up / down |
| 13 / 14 | throttle up / down | Increase / decrease throttle |
| 15 | reverse thrust | Reverse thrust (hold) |

If pitch or roll goes the wrong way, tick "reverse axis" on that binding in MSFS. For the throttle,
use Settings, Throttle, Invert in the app instead.

You only have to do this once. MSFS saves the bindings in a profile.

## Settings worth knowing

- Signed reverse thrust (Settings, Throttle): use this for aircraft that have reverse below idle on the
  same lever. You then also set where idle sits. On airliners with an idle detent it's well below the
  middle of the axis, so adjust it until the phone's idle puts the sim's lever exactly at idle.
- Autothrottle: while it's on, the throttle lever shows an A/T badge and the server stops moving the
  vJoy throttle to match the sim, so it doesn't knock autothrottle off.
- Calibration (Settings, Calibration): dead zone, response curve (expo), invert and smoothing for each
  axis. Changes apply right away and the server saves them to `config.json` next to the exe.

## SimConnect input mode

Instead of vJoy, the server can send every control as a SimConnect event. Pick `simconnect` when the
server asks, or edit `start-mhotas.bat` and add `--input=simconnect`. You don't need vJoy or any MSFS
bindings in this mode. The two modes never mix: if vJoy
is selected and isn't set up right, the server stops and tells you what's missing instead of switching
modes on its own.

## Troubleshooting

- The app says it's connecting and never connects: make sure the phone and PC are on the same network
  and that Windows Firewall allows mhotas-server. Guest Wi-Fi networks often block devices from
  talking to each other.
- The server exits saying a vJoy axis or button is missing: open Configure vJoy and check device 1 has
  X, Y, Z, Rz and 15 buttons.
- Controls do nothing in the sim: check the bindings in MSFS are on the vJoy Device, not your keyboard
  or another controller profile.
- If the phone loses connection mid-flight, pitch, roll and rudder go back to neutral after about half
  a second and the throttle stays where it was.

## Development

The repo has three parts:

- `server/`: the Go server for the PC. It talks to SimConnect and vJoy and listens for the phone over UDP.
- `app/`: the Flutter app for Android and iOS.
- `tools/udp-fake-phone/`: sends fake phone packets so you can test the server without a phone or MSFS.

The UDP protocol between the app and the server is described in [docs/protocol.md](docs/protocol.md).

You need Go 1.25 or newer and the Flutter SDK. The server only talks to the sim on Windows, but it
builds anywhere, and the `log` backend runs without MSFS:

```sh
cd server
go run ./cmd/mhotas-server --backend=log

# in another terminal
cd tools/udp-fake-phone
go run . --target=127.0.0.1:9750
```

For the app, use a real phone, since emulators don't have useful motion sensors:

```sh
cd app
flutter pub get
flutter test
flutter run
```

Pushing a tag like `v0.1.0-alpha.1` builds the APK and the Windows server and attaches them to a
GitHub release.

## License

MIT
