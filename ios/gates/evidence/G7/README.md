# Gate G7 evidence — M7 tunnel groundwork (author's side)

Collected by the author on 2026-10-12 on the dev Mac (Xcode 27.0, iOS 27.0
Simulator "iPhone 18 Pro" runtime 24A434, Go 1.27.1) on
`feat/skywire-mobile-ios` at `612b0abd6`. The gate record
(`ios/gates/G7.md`) is the reviewer's to write.

| File | What it is |
|---|---|
| `probe.log` | item 7.0: `TunnelProbeTests` on the Simulator, every step of a tunnel start and what iOS answered |
| `go-tun-tests.log` | items 7.4 and 7.5: `go test ./pkg/vpn/ -run 'IOSTunDevice\|TunSettings' -count=1 -v` |
| `coreclient-tests.log` | item 7.3 and the tunnel settings: the `ProviderMessageTests` and `TunnelSettingsTests` cases, and the full `swift test` count |

The extension's CI build and the release rehearsal are on PR
skycoin/skywire#5309 (the `iOS app` and `iOS release` runs at the pushed
head).

## What each item was checked with

- **7.0 Probe.** The answer is no: on the Xcode 27 Simulator,
  `NETunnelProviderManager.loadAllFromPreferences()` and
  `saveToPreferences()` both fail at once with `NEVPNErrorDomain 5: IPC
  failed`, so nothing can be installed or started. This matches the runtime's
  contents: none of its 329 launch daemons is the VPN session manager
  (`nesessionmanager`, `nehelper` or `neagent`). The App Group container does
  exist on the Simulator. D2 and D3 stay in Lane D.
- **7.1 PacketTunnel.** A new app-extension target, embedded in the app and
  linking CoreBridge and CoreClient. It builds three ways: inside the app for
  the Simulator, alone for the Simulator (signed to run locally), and alone for
  a generic iOS device with `CODE_SIGNING_ALLOWED=NO`, which ios-app.yml now
  runs on every PR. The simulated entitlements Xcode signs with
  (`*-Simulated.xcent`) carry `packet-tunnel-provider`, the App Group and, for
  the extension, only the shared Keychain group. An unsigned Release archive
  embeds `PacketTunnel.appex` with the app's version and build number, and
  both binaries contain the Go runtime.
- **7.2 TunnelCoreHost.** It prepares the config in the App Group container,
  installs and enables the VPN configuration, starts the tunnel, and maps
  `NEVPNStatus` to the core's state. The probe drives its `installedManager`.
  `CoreHostChoice` keeps the Simulator in the app; the launch argument
  `-SkywireCoreHost` and a debug-build card in Settings > Logs & diagnostics
  override it. The Skywire scheme's tests pass with the app's new
  entitlements, the Keychain tests included.
- **7.3 ProviderMessageTransport.** CoreClient against the recorded API
  through both ends: a first run's account and login, the session cookie, CSRF
  tokens, an attachment up and one down; a refused connection keeps its
  URLError code; a stream fails at once.
- **7.4 and 7.5 TUN.** The utun framing is in a file without a build tag, run
  over a datagram socket pair: packets from the kernel lose their 4-byte
  header, packets written gain it, AF_INET 2 and Darwin's AF_INET6 30 as a
  big-endian uint32. The playbook said host byte order; wireguard-go's
  tun_darwin.go writes it big-endian, and the test fails with either the
  family or the byte order changed. `SetupTUN` records the address the server
  assigned instead of being a no-op, because the extension must apply that
  address; `skywire_tun_settings` returns it. `ios-deps-check` is clean (936
  packages, one fewer: the darwin shell-out code left the iOS build).
- **7.6 README.** Rewritten from M2's: where the core runs and the probe,
  the extension build, the Apple setup list, troubleshooting.
- **7.7 Release rehearsal.** `workflow_dispatch` is not possible yet: GitHub
  dispatches only workflows on the default branch, and ios-release.yml is not
  on develop. The PR that changes the file runs it instead, to the
  missing-secrets notice. Its new archive checks were tried against a local
  unsigned archive (they pass, and fail on a mismatched build number); the
  entitlement lines need a signed archive.

## `#if targetEnvironment(simulator)`

One occurrence, `ios/Skywire/Core/TunnelCoreHost.swift`
(`CoreHostChoice.current`): the Simulator runs the core in the app because it
has no VPN service (the probe above).

## Found on the way, for Lane D

- **The core is in the app twice.** The app links the core for configuration
  and the in-app host, and the extension links it to run: a Release archive is
  111 MB, 53 MB of it the extension. Generating the config in the extension
  would let a device build drop the app's copy.
- **IPv6 is not carried by SkyVPN.** vpn-client assigns an IPv4 address only,
  so the tunnel's settings route all IPv4; IPv6 traffic leaves outside it.
- **Unverified until a device:** `UtunDescriptor.find()` (the utun found by
  `getsockopt(UTUN_OPT_IFNAME)`, as WireGuard does), the tunnel's start and
  settings, loopback between app and extension (D2), and the extension's
  memory (D3).
