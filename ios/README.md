# Skywire iOS

The native iOS app for Skywire: a Swift / SwiftUI shell around the
**Skywire mobile core**, the same Go core the Android app ships as
`libskywire-mobile.so` (visor, config generation, and the four client apps
skychat, skysocks-client, vpn-client and skydex-client, all in-process). On
iOS the core is linked into the app as a static library instead of being
exec'd, because iOS has no `fork`/`exec`; everything above that boundary
drives the visor through its authenticated local REST API on
`127.0.0.1:8000`, exactly as the Android app does.

## Status

The Go side is in (milestone M0): `make ios-core` builds
`SkywireCore.xcframework` from `cmd/skywire-mobile-core`, and `pkg/mobilecore`
starts, stops and restarts the lite core inside one process. Milestone M1
adds the Xcode project: the `CoreBridge` package over the core's C API, an
in-app core host, and a single spike screen (Connect, state, `/api/ping`, the
process footprint, a log tail). The real screens, CI lanes and the rest land
milestone by milestone from M2 on.
Gate records and their evidence live in `gates/`. The planning documents
(the two briefs, the proposal and the playbook) live in this folder on the
maintainer's machine and are gitignored on purpose; ask for them if you need
the background.

The port is developed and verified on the Xcode Simulator until the end,
and the device-only parts (the packet-tunnel extension, SkyVPN, signing,
TestFlight) follow once an Apple developer account exists.

## Design in one paragraph

The Go core is built with `-buildmode=c-archive` for the iOS device and
Simulator slices and packaged as `SkywireCore.xcframework` (a build output,
never committed). A C API of about eight functions covers lifecycle only:
start, stop, state, last error, config generation, TUN hand-off, log sink,
free. One Swift file (`CoreBridge`) calls it; the rest of the app uses the
local HTTP API through a Swift port of the Android client. On devices the
core runs inside the packet-tunnel extension, which is the only iOS process
that can host a VPN and stay alive in the background; on the Simulator, where
extensions do not run, the same core runs in the app process. The wallet is a
Swift port of the Kotlin `android/wallet-core` module and does not touch Go.

## Layout

```
ios/
├── README.md
├── Skywire.xcodeproj            # hand-maintained, no generator; folders are synchronized groups
├── Config/Shared.xcconfig       # bundle id, groups, URL scheme, versions (one place)
├── Skywire/                     # app target
│   ├── Core/                    # CoreHost, InAppCoreHost, ConfigProfile, CoreLog, Footprint
│   └── Spike/                   # the M1 screen
├── SkywireTests/                # XCTest, no host app: the core runs in the test process
├── Packages/
│   └── CoreBridge/              # the only code that sees the C API (module SkywireCore)
├── gates/                       # milestone gate records and their evidence (text only)
└── Frameworks/SkywireCore.xcframework   # build output of make ios-core, gitignored
```

Still to come: `PacketTunnel/` (the NetworkExtension target that hosts the
core on devices), `Packages/CoreClient` (the local-API client) and
`Packages/WalletCore` (the Swift port of `android/wallet-core`).

## Building and running

Everything runs from the repo root, as for Android. Xcode 27 or later with
an iOS Simulator runtime; Go as in `go.mod`.

```sh
make ios-core        # Go core → ios/Frameworks/SkywireCore.xcframework (device + Simulator arm64 slices)
make mobile-test     # pkg/mobilecore: start/stop/start in one process, on the lite module set

# app + tests on the Simulator (no signing)
xcodebuild -project ios/Skywire.xcodeproj -scheme Skywire \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' \
  -derivedDataPath ios/DerivedData test CODE_SIGNING_ALLOWED=NO

# run it
xcrun simctl boot "iPhone 18 Pro"
xcodebuild -project ios/Skywire.xcodeproj -scheme Skywire \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' \
  -derivedDataPath ios/DerivedData build CODE_SIGNING_ALLOWED=NO
xcrun simctl install booted ios/DerivedData/Build/Products/Debug-iphonesimulator/Skywire.app
xcrun simctl launch booted com.skycoin.skywire
curl -s http://127.0.0.1:8000/api/ping     # "PONG!" once Connect has finished
xcrun simctl spawn booted log stream --level info --predicate 'subsystem == "com.skycoin.skywire"'
```

`make ios-core` needs Xcode (macOS); `IOS_SIM_X86_64=1` adds an Intel
Simulator slice. The core's C API is eight functions, documented in
`cmd/skywire-mobile-core/main.go` and declared in the generated
`skywire_core.h` inside the xcframework, which also carries the module map
that makes it the Swift module `SkywireCore`.

Rule of thumb, same as Android: whenever the Go side changed, run
`make ios-core` first; Xcode's Run does not rebuild the core. Without the
xcframework the project does not open cleanly: the `CoreBridge` package
points at it.

The Simulator shares the Mac's loopback, so `curl http://127.0.0.1:8000/api/ping`
from the Mac reaches the app's visor, and a desktop visor or the docker e2e
lane on the same ports clashes with it. Stop those before a Simulator
session. The tests bind 127.0.0.1:8000 as well, so the app must not be
connected while they run.

Notes:

- **The core's log** goes to the unified log (subsystem = the bundle ID,
  category `core`), and the spike screen shows its last 200 lines.
  The process footprint is logged once a minute (category `footprint`).
- **Connect is remembered.** A relaunch after a force-quit connects again
  if the core was left connected, like Android's sticky core service.
- **Memory limit.** The profile writes `memory_limit: "40MiB"`, the value
  the packet-tunnel extension will need. To measure without it, launch with
  `xcrun simctl launch booted com.skycoin.skywire -SkywireMemoryLimit none`
  (a launch argument, so it lasts one launch).
- **Debugging from Xcode.** The Go runtime uses signals (`SIGURG` for
  preemption, `SIGPIPE`). If LLDB stops on them, run `process handle SIGURG
  SIGPIPE -n false -p true -s false` in the debugger console.

## Releasing (planned)

iOS shares the Android tag line: pushing `mobile-vX.Y.Z` runs
`android-release.yml` and `ios-release.yml` side by side, and neither
blocks the other. `mobile-v0.x` builds go to TestFlight; `mobile-v1.x`
builds are uploaded to App Store Connect and a person submits them for
review, so no tag can publish to the App Store by itself. Until the App
Store Connect secrets exist, the iOS job builds and tests for the Simulator
and stops with a notice. Because a `mobile-v*` tag also releases Android,
the iOS workflow is rehearsed with `workflow_dispatch`, never with a tag.

Required secrets, by name only: `APP_STORE_CONNECT_KEY_ID`,
`APP_STORE_CONNECT_ISSUER_ID`, `APP_STORE_CONNECT_PRIVATE_KEY`; variable
`APPLE_TEAM_ID`. No certificate, profile or key is ever committed.

## License

Part of Skywire, licensed under the [GNU Affero General Public License v3.0](../LICENSE)
(AGPL-3.0-only); a commercial license is also available — see
[COMMERCIAL-LICENSE.md](../COMMERCIAL-LICENSE.md).
