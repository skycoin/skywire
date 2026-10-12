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

The app does on the Simulator what the Android app does: Home and Connect,
SkyChat with calls, SkySOCKS, SkyDEX, Skymail, Fleet and remote management,
the wallet, Settings and diagnostics, in English, Simplified Chinese and
Spanish. Each milestone passed an independent gate review; the records and
their evidence are in `gates/`. M7 adds what the device needs and the
Simulator cannot run: the packet-tunnel extension (`PacketTunnel/`), the host
that starts it (`TunnelCoreHost`), the provider-message transport, and
vpn-client's iOS TUN in Go. Running the extension, SkyVPN, signing and
TestFlight need an Apple developer account (the playbook's Lane D).

The planning documents (the two briefs, the proposal and the playbook) live in
this folder on the maintainer's machine and are gitignored on purpose; ask for
them if you need the background.

## Design in one paragraph

The Go core is built with `-buildmode=c-archive` for the iOS device and
Simulator slices and packaged as `SkywireCore.xcframework` (a build output,
never committed). A C API of nine functions covers lifecycle only: start,
stop, state, last error, config generation, the TUN hand-off and the tunnel
settings vpn-client asks for, log sink, free. One Swift file (`CoreBridge`) calls it; the rest of the app uses the
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
│   ├── Core/                    # CoreHost (InAppCoreHost, TunnelCoreHost), ConfigProfile, settings, lock, logs, calls, wallet stores
│   ├── Model/                   # AppModel: the core's lifecycle, the session, Connect
│   ├── Views/                   # Home, Chat, Apps hub, SOCKS, DEX, Mail, Fleet, Wallet, calls, Settings, Logs
│   ├── Localizable.xcstrings    # en, zh-Hans, es; written by scripts/seed-strings.py
│   ├── InfoPlist.xcstrings      # the permission prompts, translated
│   ├── PrivacyInfo.xcprivacy    # required-reason APIs, no tracking, no collection
│   └── Skywire.entitlements     # Keychain groups, the App Group, the packet tunnel
├── PacketTunnel/                # the NetworkExtension target that hosts the core on a device
├── SkywireTests/                # XCTest, no host app: the core runs in the test process
├── SkywireAppTests/             # XCTest hosted by the app: what needs its entitlements (Keychain)
├── SkywireUITests/              # the screen tour and the gate's flows (SkywireTour scheme, not CI)
├── Packages/
│   ├── CoreBridge/              # the only code that sees the C API (module SkywireCore)
│   ├── CoreClient/              # the local-API client, the phone profile, provider messages; `swift test` on the Mac
│   └── WalletCore/              # the Swift port of android/wallet-core; `swift test` on the Mac
├── scripts/                     # CI helpers, the string seeder, the API fixture recorder
├── gates/                       # milestone gate records and their evidence (text only)
└── Frameworks/SkywireCore.xcframework   # build output of make ios-core, gitignored
```

## Building and running

Everything runs from the repo root, as for Android. Xcode 27 or later with
an iOS Simulator runtime; Go as in `go.mod`.

```sh
make ios-core        # Go core → ios/Frameworks/SkywireCore.xcframework (device + Simulator arm64 slices)
make mobile-test     # pkg/mobilecore: start/stop/start in one process, on the lite module set (about 10 minutes: it dials the live network)
(cd ios/Packages/CoreClient && swift test)   # API client, phone profile, provider messages: on the Mac, no core
(cd ios/Packages/WalletCore && swift test)   # the wallet against the Kotlin vectors
go test ./pkg/vpn/ -run 'IOSTunDevice|TunSettings' -count=1   # vpn-client's iOS TUN framing, on the Mac

# the packet-tunnel extension, for a device and unsigned: it cannot run on the Simulator
xcodebuild -project ios/Skywire.xcodeproj -scheme PacketTunnel \
  -destination 'generic/platform=iOS' -derivedDataPath ios/DerivedData \
  CODE_SIGNING_ALLOWED=NO build

# app + tests on the Simulator. The call tests hold the microphone, so grant
# it first: a new or erased device has not, and simctl grants only while booted.
xcrun simctl bootstatus "iPhone 18 Pro" -b
xcrun simctl privacy "iPhone 18 Pro" grant microphone com.skycoin.skywire
xcodebuild -project ios/Skywire.xcodeproj -scheme Skywire \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' \
  -derivedDataPath ios/DerivedData test

# run it
xcrun simctl bootstatus "iPhone 18 Pro" -b
xcodebuild -project ios/Skywire.xcodeproj -scheme Skywire \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' \
  -derivedDataPath ios/DerivedData build
xcrun simctl install "iPhone 18 Pro" ios/DerivedData/Build/Products/Debug-iphonesimulator/Skywire.app
xcrun simctl launch "iPhone 18 Pro" com.skycoin.skywire -core.wantsConnected YES   # connects as if Connect were tapped
curl -s http://127.0.0.1:8000/api/ping     # "PONG!" once the core has started, 30 to 60 s
xcrun simctl spawn "iPhone 18 Pro" log stream --level info --predicate 'subsystem == "com.skycoin.skywire"'
```

The commands name the device rather than `booted`, which is ambiguous once
two Simulators are booted. `xcrun simctl bootstatus -b` boots the device if
it is not booted and waits until it is. Xcode 27 has no Simulator.app; to see
the device and tap the app, open
`/Applications/Xcode.app/Contents/Applications/DeviceHub.app`.

**Simulator builds are signed to run locally**, not unsigned: the project
signs them with the ad-hoc identity `-` (no Apple account, no certificate,
works on CI too). An unsigned app (`CODE_SIGNING_ALLOWED=NO`, as M1 built it)
has no entitlements and every Keychain call fails with
errSecMissingEntitlement, so the app cannot keep its passwords. Do not add
that flag to a Simulator build; the device build of the extension above only
proves it compiles and links.

`make ios-core` needs Xcode (macOS); `IOS_SIM_X86_64=1` adds an Intel
Simulator slice. The core's C API is nine functions, documented in
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
(skysocks-client), 5505 (the visor's app launcher). Stop those before a
Simulator session. The unhosted tests
bind 127.0.0.1:8000 as well, so the app must not be connected while they run.

### Tests

| Where | What | Runs |
|---|---|---|
| `Packages/CoreClient` (`swift test`) | every API route the app uses against answers recorded from a real core, the session rules (first-run account, one re-login on 401, CSRF), the transport (streams, timeouts), the same API over provider messages, the tunnel settings, the phone profile against the Android app's own output, the log parser | Mac, CI |
| `Packages/WalletCore` (`swift test`) | the wallet against the Kotlin module's vectors and fixtures | Mac, CI |
| `SkywireTests` | the core started and stopped through CoreBridge (it dials the real network); the string catalogues | Simulator, CI |
| `SkywireAppTests` | SecretStore in the real Keychain, the wallet store, calls, the config regenerate, the diagnostics logs; `TunnelProbeTests` only on request (below) | Simulator (hosted by the app), CI |
| `SkywireUITests` (scheme `SkywireTour`) | the screen tour in the three languages, restart from Home, the Fleet switch, Skymail (to itself, and both ways with a desktop peer) | Simulator, by hand |

The screen tour and its screenshots:

```sh
xcodebuild -project ios/Skywire.xcodeproj -scheme SkywireTour \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' \
  -resultBundlePath ios/build/Tour.xcresult test -only-testing:SkywireUITests/ScreenTour
xcrun xcresulttool export attachments --path ios/build/Tour.xcresult --output-path <dir>
```

`MailChecks/testExchangeWithAPeer` needs a second visor on the Mac whose API
answers on another port without a login (the desktop peer: `make build-mobile`,
`hypervisor.http_addr` 127.0.0.1:18000, `enable_auth` false). Name it to the runner:

```sh
TEST_RUNNER_SKYWIRE_MAIL_PEER=http://127.0.0.1:18000 TEST_RUNNER_SKYWIRE_MAIL_PEER_PK=<its pk> \
  xcodebuild … -scheme SkywireTour test -only-testing:SkywireUITests/MailChecks
```

## Where the core runs

On a device the core runs in the packet-tunnel extension, the only process
iOS keeps running in the background (`TunnelCoreHost`). A start writes the
config into the App Group container (`group.com.skycoin.skywire`), installs
the app's VPN configuration (iOS asks the user once) and starts the tunnel;
the extension starts the core on that config. The app reaches the extension's
API on `127.0.0.1:8000`. If a device turns out not to allow that, the same
requests can travel as provider messages (`ProviderMessageTransport`), one
request per message; streams cannot.

On the Simulator the core runs in the app (`InAppCoreHost`, data under
Application Support), because the Simulator has no VPN service: every
`NETunnelProviderManager` call fails with `NEVPNErrorDomain 5: IPC failed`.
That was measured with Xcode 27 and the iOS 27.0 runtime by the probe, which
installs a VPN configuration and removes it again:

```sh
TEST_RUNNER_SKYWIRE_TUNNEL_PROBE=1 xcodebuild -project ios/Skywire.xcodeproj -scheme Skywire \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' -derivedDataPath ios/DerivedData \
  test -only-testing:SkywireAppTests/TunnelProbeTests
```

`-SkywireCoreHost app|tunnel` as a launch argument, or Settings > Logs &
diagnostics in a debug build, chooses the host at the next launch: the app on
a device for debugging, the tunnel on the Simulator for the probe. The two
hosts keep separate data directories, so each has its own visor identity.

SkyVPN in the extension: the extension hands its utun descriptor to the core
(`skywire_set_tun_fd`), and vpn-client reads and writes it with the utun
framing (`pkg/vpn/tun_device_ios.go`). vpn-client cannot configure the
interface itself, so it records the address the server assigned
(`skywire_tun_settings`); the extension reads that once a second and applies
it as its tunnel settings. While SkyVPN is off the tunnel routes nothing;
while it is on it carries all IPv4. IPv6 is not carried yet.

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
  `xcrun simctl launch "iPhone 18 Pro" com.skycoin.skywire -SkywireMemoryLimit none`
  (a launch argument, so it lasts one launch).
- **Language.** The app follows iOS's per-app language (Settings > Skywire >
  Language). To launch in one: `xcrun simctl launch "iPhone 18 Pro" com.skycoin.skywire
  -AppleLanguages '(zh-Hans)'`.
- **Debugging from Xcode.** The Go runtime uses signals (`SIGURG` for
  preemption, `SIGPIPE`). If LLDB stops on them, run `process handle SIGURG
  SIGPIPE -n false -p true -s false` in the debugger console.

## CI

`.github/workflows/ios-app.yml` runs on pull requests that touch `ios/`,
`pkg/mobilecore`, `cmd/skywire-mobile-core`, `pkg/vpn` or the Makefile: it
selects the newest Xcode 27, or while GitHub's images do not carry it yet the
newest Xcode 26 (`ios/scripts/ci-xcode.sh`, failing loudly when neither is
there), builds the xcframework, runs `swift test` in `CoreClient` and
`WalletCore`, the Skywire scheme's tests on a Simulator, and the extension's
device build, and keeps the result bundle when something fails. Nothing is
signed with an Apple identity.

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
`APPLE_TEAM_ID`. No certificate, profile or key is ever committed. Before it
uploads, the job checks the archive: the app and the extension carry the same
version and build number, both have the packet-tunnel entitlement, and both
binaries contain the Go core.

### Apple setup, once the account exists

All of it in the Apple developer portal and App Store Connect, by hand:

- an Organization developer account (VPN apps, guideline 5.4, and crypto
  wallets, 3.1.5, need one);
- App IDs for the app (`com.skycoin.skywire`) and the extension
  (`com.skycoin.skywire.tunnel`), with Network Extension (packet tunnel), App
  Groups (`group.com.skycoin.skywire`) and Keychain sharing;
- the App Store Connect app record;
- an App Store Connect API key with a role that can manage signing, stored as
  the three secrets above, and the team ID as `APPLE_TEAM_ID`;
- TestFlight groups and the public link;
- the Beta App Review information;
- the privacy policy URL and the export-compliance answers.

The identifiers are provisional until then, all in `Config/Shared.xcconfig`.

## Troubleshooting

- **errSecMissingEntitlement (-34018) from every Keychain call**: the
  Simulator build was unsigned. Drop `CODE_SIGNING_ALLOWED=NO`; the project
  signs Simulator builds to run locally.
- **VoiceAudioReleaseTests fails with "the microphone is not granted"**: the
  device is new or was erased. Boot it and grant the microphone as in Building
  and running; `simctl privacy` refuses a device that is shut down ("Unable to
  lookup in current state: Shutdown").
- **The core refuses its config ("version")**: a config written by an untagged
  build is refused by a tagged core. The first launch of each new build
  regenerates the config with the same key (`ConfigProfile`); an install from
  before that is fixed by launching a current build once.
- **"found architecture 'arm64', required architecture 'x86_64'"**: the
  xcframework has arm64 Simulator slices only. Build for a named Simulator,
  not `generic/platform=iOS Simulator`, or run `IOS_SIM_X86_64=1 make ios-core`.
- **No Simulator runtime**: `xcodebuild -downloadPlatform iOS` fetches one
  (several GB).
- **Port in use**: the Simulator shares the Mac's loopback; stop what holds
  8000, 8001, 8051, 1080 or 5505 (see Building and running).
- **Build number rejected by App Store Connect**: every upload needs a new
  one. The release job uses `<run_number>.<run_attempt>`, so a re-run never
  reuses a number; a local archive must set `CURRENT_PROJECT_VERSION` higher
  than the last upload, for the app and the extension alike.

## License

Part of Skywire, licensed under the [GNU Affero General Public License v3.0](../LICENSE)
(AGPL-3.0-only); a commercial license is also available — see
[COMMERCIAL-LICENSE.md](../COMMERCIAL-LICENSE.md).
