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

Planning. Nothing in this directory builds yet; the Xcode project, the Go
build target and the CI lanes land milestone by milestone. This README is
the only tracked file. The planning documents (the two briefs, the
proposal and the playbook) live in this folder on the maintainer's machine
and are gitignored on purpose; ask for them if you need the background.

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

## Planned layout

```
ios/
├── README.md
├── Skywire.xcodeproj
├── Config/                      # xcconfig: bundle id, groups, versions (one place)
├── Skywire/                     # app target: App, Views, ViewModels, Services, Resources
├── PacketTunnel/                # NetworkExtension target: hosts the core on devices
├── Packages/
│   ├── CoreBridge/              # C interop + module map (the only place that sees C)
│   ├── CoreClient/              # local-API client + models, tested against a stub server
│   └── WalletCore/              # Swift port of android/wallet-core, tested with its vectors
├── SkywireTests/
├── gates/                       # milestone gate records (text only)
└── Frameworks/SkywireCore.xcframework   # build output, gitignored
```

## Building and running (planned)

Everything runs from the repo root, as for Android:

```sh
make ios-core        # Go core → ios/Frameworks/SkywireCore.xcframework (device + Simulator slices)
xcodebuild -project ios/Skywire.xcodeproj -scheme Skywire \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' test CODE_SIGNING_ALLOWED=NO
```

Rule of thumb, same as Android: whenever the Go side changed, run
`make ios-core` first; Xcode's Run does not rebuild the core.

The Simulator shares the Mac's loopback, so `curl http://127.0.0.1:8000/api/ping`
from the Mac reaches the app's visor, and a desktop visor or the docker e2e
lane on the same ports clashes with it. Stop those before a Simulator
session.

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
