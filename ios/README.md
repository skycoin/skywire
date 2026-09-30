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
starts, stops and restarts the lite core inside one process. M1 added the
Xcode project, the `CoreBridge` package over the core's C API and an in-app
core host. M2 makes it an app: the `CoreClient` package (the local-API client,
the phone profile, the Keychain store), Home, Settings and the log viewer in
English, Simplified Chinese and Spanish, an app lock, the privacy manifest, and
the two CI lanes. Chat, the other apps and the wallet follow milestone by
milestone.
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
│   ├── Core/                    # CoreHost, InAppCoreHost, ConfigProfile, AppSettings, AppLock, CoreLog, Footprint
│   ├── Model/                   # AppModel: the core's lifecycle, the session, Connect
│   ├── Views/                   # Home, Settings (+ Diagnostics), Logs, shared components
│   ├── Localizable.xcstrings    # en, zh-Hans, es; written by scripts/seed-strings.py
│   ├── InfoPlist.xcstrings      # the Face ID prompt, translated
│   ├── PrivacyInfo.xcprivacy    # required-reason APIs, no tracking, no collection
│   └── Skywire.entitlements     # the Keychain access group (shared with the extension later)
├── SkywireTests/                # XCTest, no host app: the core runs in the test process
├── SkywireAppTests/             # XCTest hosted by the app: what needs its entitlements (Keychain)
├── SkywireUITests/              # the screen tour and the gate's flows (SkywireTour scheme, not CI)
├── Packages/
│   ├── CoreBridge/              # the only code that sees the C API (module SkywireCore)
│   └── CoreClient/              # the local-API client, the phone profile, SecretStore; `swift test` on the Mac
├── scripts/                     # CI helpers, the string seeder, the API fixture recorder
├── gates/                       # milestone gate records and their evidence (text only)
└── Frameworks/SkywireCore.xcframework   # build output of make ios-core, gitignored
```

Still to come: `PacketTunnel/` (the NetworkExtension target that hosts the
core on devices) and `Packages/WalletCore` (the Swift port of
`android/wallet-core`).

## Building and running

Everything runs from the repo root, as for Android. Xcode 27 or later with
an iOS Simulator runtime; Go as in `go.mod`.

```sh
make ios-core        # Go core → ios/Frameworks/SkywireCore.xcframework (device + Simulator arm64 slices)
make mobile-test     # pkg/mobilecore: start/stop/start in one process, on the lite module set
(cd ios/Packages/CoreClient && swift test)   # API client, phone profile, log parser: on the Mac, no core

# app + tests on the Simulator
xcodebuild -project ios/Skywire.xcodeproj -scheme Skywire \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' \
  -derivedDataPath ios/DerivedData test

# run it
xcrun simctl boot "iPhone 18 Pro"
xcodebuild -project ios/Skywire.xcodeproj -scheme Skywire \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' \
  -derivedDataPath ios/DerivedData build
xcrun simctl install booted ios/DerivedData/Build/Products/Debug-iphonesimulator/Skywire.app
xcrun simctl launch booted com.skycoin.skywire
curl -s http://127.0.0.1:8000/api/ping     # "PONG!" once Connect has finished
xcrun simctl spawn booted log stream --level info --predicate 'subsystem == "com.skycoin.skywire"'
```

**Simulator builds are signed to run locally**, not unsigned: the project
signs them with the ad-hoc identity `-` (no Apple account, no certificate,
works on CI too). An unsigned app (`CODE_SIGNING_ALLOWED=NO`, as M1 built it)
has no entitlements and every Keychain call fails with
errSecMissingEntitlement, so the app cannot keep its passwords. Do not add
that flag to the commands above.

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
from the Mac reaches the app's visor, and anything on the Mac holding the
app's ports clashes with it: 8000 (the API: a desktop visor), 8001 (skychat:
the docker e2e lane publishes it through colima), 8051 (skydex-client), 1080
(skysocks-client). Stop those before a Simulator session. The unhosted tests
bind 127.0.0.1:8000 as well, so the app must not be connected while they run.

### Tests

| Where | What | Runs |
|---|---|---|
| `Packages/CoreClient` (`swift test`) | every M2 API route against answers recorded from a real core, the session rules (first-run account, one re-login on 401, CSRF), the transport (streams, timeouts), the phone profile against the Android app's own output, the log parser | Mac, CI |
| `SkywireTests` | the core started and stopped through CoreBridge (it dials the real network); the string catalogues | Simulator, CI |
| `SkywireAppTests` | SecretStore in the real Keychain, with the app's access group | Simulator (hosted by the app), CI |
| `SkywireUITests` (scheme `SkywireTour`) | the screen tour in the three languages, restart from Home, the Fleet switch | Simulator, by hand |

The screen tour and its screenshots:

```sh
xcodebuild -project ios/Skywire.xcodeproj -scheme SkywireTour \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' \
  -resultBundlePath ios/build/Tour.xcresult test -only-testing:SkywireUITests/ScreenTour
xcrun xcresulttool export attachments --path ios/build/Tour.xcresult --output-path <dir>
```

### Strings

The app's keys are the Android app's (`android/app/src/main/res/values*/strings.xml`),
so both apps say the same thing in the same words. After adding or removing a
string in the Swift sources, run `ios/scripts/seed-strings.py`: it writes
`Localizable.xcstrings` with exactly the keys the sources use, the three
languages taken from the Android catalogues (`%1$s` becomes `%1$@`, `%1$d`
`%1$lld`), and the iOS-only keys from `ios/scripts/strings-ios.json`.
`StringCatalogTests` fails when a key is missing, unused, or has a language
or a placeholder missing. In views use `Text("key")`; a key chosen at run
time goes through `L10n.key("key")` written as a literal (the scan finds keys
by reading the sources), formatted strings through `L10n.format`.

### API fixtures

`Packages/CoreClient/Tests/Fixtures/routes/` holds what a real core answers on
each route; `ios/scripts/record-api-fixtures.sh` re-records them against a
throwaway host core (`make build-mobile`) and scrubs the recording machine out.
Re-record after an API change and review the diff.

Notes:

- **The core's log** goes to the unified log (subsystem = the bundle ID,
  category `core`) and to the log viewer's Process source.
  The process footprint is logged once a minute (category `footprint`) and
  shown under Settings > Logs & diagnostics.
- **Connect is remembered.** A relaunch after a force-quit connects again
  if the core was left connected, like Android's sticky core service.
- **Memory limit.** The profile writes `memory_limit: "40MiB"`, the value
  the packet-tunnel extension will need. To measure without it, launch with
  `xcrun simctl launch booted com.skycoin.skywire -SkywireMemoryLimit none`
  (a launch argument, so it lasts one launch).
- **Language.** The app follows iOS's per-app language (Settings > Skywire >
  Language). To launch in one: `xcrun simctl launch booted com.skycoin.skywire
  -AppleLanguages '(zh-Hans)'`.
- **Debugging from Xcode.** The Go runtime uses signals (`SIGURG` for
  preemption, `SIGPIPE`). If LLDB stops on them, run `process handle SIGURG
  SIGPIPE -n false -p true -s false` in the debugger console.

## CI

`.github/workflows/ios-app.yml` runs on pull requests that touch `ios/`,
`pkg/mobilecore`, `cmd/skywire-mobile-core` or the Makefile: it selects Xcode
27 (`ios/scripts/ci-xcode.sh`, failing loudly when the runner image lacks it),
builds the xcframework, runs `swift test` in `CoreClient` and the Skywire
scheme's tests on a Simulator, and keeps the result bundle when something
fails. Nothing is signed with an Apple identity.

## Releasing

iOS shares the Android tag line: pushing `mobile-vX.Y.Z` runs
`android-release.yml` and `ios-release.yml` side by side, and neither
blocks the other. `mobile-v0.x` builds go to TestFlight; `mobile-v1.x`
builds are sent to App Store Connect and a person submits them for review,
so no tag can publish to the App Store by itself. The version is X.Y.Z and the
build number `<run_number>.<run_attempt>` of the workflow run.

Until the App Store Connect secrets exist, the iOS job runs the Go core's
tests, builds the xcframework, builds and tests the app on a Simulator with the
version stamped, and stops green with a notice naming what is missing.
Because a `mobile-v*` tag also releases Android, the iOS workflow is
rehearsed without one: the pull request that changes `ios-release.yml` runs
it, and once the file is on the default branch (where GitHub looks for
dispatchable workflows) so does

```sh
gh workflow run ios-release.yml -f ref=<branch> -f version=0.1.0
```

A rehearsal never reaches App Store Connect, unless it is a dispatch with
`deliver` ticked (and the secrets exist).

Required secrets, by name only: `APP_STORE_CONNECT_KEY_ID`,
`APP_STORE_CONNECT_ISSUER_ID`, `APP_STORE_CONNECT_PRIVATE_KEY`; variable
`APPLE_TEAM_ID`. No certificate, profile or key is ever committed.

## License

Part of Skywire, licensed under the [GNU Affero General Public License v3.0](../LICENSE)
(AGPL-3.0-only); a commercial license is also available — see
[COMMERCIAL-LICENSE.md](../COMMERCIAL-LICENSE.md).
