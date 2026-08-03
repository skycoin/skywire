# Implementation report — Skywire Android

Running log of what was implemented, when, and how it was verified.
After finishing a part, add a dated entry at the top: what was built, key
decisions/deviations discovered while building it, and the verification that
was actually performed (commands, devices, measured numbers — not intentions).

---

## 2026-08-03 — Core service, on-device config, the Connect screen, log viewer

**Built:**

- `core/SkywireCoreService` — `specialUse` foreground service that execs the
  Go payload from `nativeLibraryDir` as a child process: rotating capture of
  its combined output (`files/skywire/skywire-process.log`), crash-restart
  with backoff (1 s → 30 s, reset after a stable minute), graceful stop.
- `core/ConfigManager` — first-run `config gen` run by the payload itself
  (keys generated on-device, `-r` keeps them across regens) + the phone
  profile applied in Kotlin on every start: API pinned `127.0.0.1:8000` with
  auth, `cli_addr` emptied (no RPC), `pty`/`skywire-tcp`/`lan_dmsg_server`
  dropped, `dmsgscp` disabled, absolute app-private paths, apps in-proc with
  autostart off.
- `core/SecretStore` — random API password satisfying the server policy,
  AES-GCM-encrypted via AndroidKeyStore, stored in DataStore.
- `api/VisorApi` — OkHttp client for the local API: `admin` account
  bootstrap, transparent re-login on 401 (visor restarts drop sessions),
  CSRF helper, summary/dmsg/service-health/runtime-logs/app-logs endpoints
  (the app-logs 500 "no new available logs" is treated as an empty page).
- Home: Connect/Disconnect state machine over the service state + live visor
  card (PK tap-to-copy, version, uptime, transports, dmsg latencies, service
  health) and log-viewer entry points. Disconnect stays reachable while the
  core is still coming up or crash-looping.
- `ui/logs/` — one viewer, three sources: core runtime ring (since-cursor
  polling incl. reset detection across visor restarts), per-app feed
  (RFC3339Nano cursor + boundary-line dedupe), and the captured process
  output (UTF-8 byte tail, rotation-aware) — the only source that works when
  the visor won't start. Follow/pause, level chips, search, tap-to-copy,
  share (capped).
- Manifest: `FOREGROUND_SERVICE_SPECIAL_USE` + `POST_NOTIFICATIONS`;
  cleartext allowed for loopback only (`network_security_config.xml`).
- Go core fixes for app-UID Android: `pkg/netutil` enumerates interfaces via
  `anet` (Android 11+ denies the stdlib's netlink dump — "netlinkrib:
  permission denied"), with the device API level passed as
  `SKYWIRE_ANDROID_API_LEVEL` because a CGO-free process cannot detect it;
  `DefaultNetworkInterface` falls back to the first routable interface when
  `ip r` prints nothing; `Visor.Ports()` no longer panics on configs without
  `pty`/`skywire-tcp`/`cli_addr`.

**Hard-won facts:**

- `launcher.bin_path` must never point into the install dir: the launcher
  MkdirAlls it at startup and the native-library path is read-only *and*
  changes on every app update — the visor then aborts ("failed to create
  dir … permission denied"). It now points at an app-private dir and the
  profile re-pins it on every launch.
- Android's `Process.destroy()` sends SIGKILL, not SIGTERM — the graceful
  stop finds the child's pid in `/proc` and delivers a real SIGTERM
  (`Os.kill`), verified by "Shutdown complete. Goodbye!" and exit 0.
- An adversarial review pass over the diff surfaced and fixed: Disconnect
  unreachable during startup/crash-loop, the auth-recovery job cancelling
  itself (it stops the core, which cancels the collector that launched it),
  spawn-failure `Failed` state being overwritten by `Stopped`, the
  runtime-log cursor silently skipping a restarted visor's lines, and the
  log ring polluting itself via per-poll session probes.

**Verified (fresh install each time, DM-B70104, Android 15, slow LTE):**
config generated on-device in ~1 s (offline, `--nofetch`) with every pin
checked field-by-field on the resulting JSON; Connect → API up in 20–120 s;
card live with all service-health rows OK; `kill -9` of the visor →
respawned in ≤ 4 s → reconnected without user action; `am kill` of the app →
visor survived (same pid) behind the foreground service; Disconnect →
SIGTERM → clean module unwind → exit 0 and the notification cleared; the
viewer live-tailed the core ring during connection and the process capture
during startup.

---

## 2026-08-03 — Brand assets: real logo, Skycoin typeface, designed top bar

**Built:**

- The real Skycoin cloud (`skywire-logo.png`, 1600², supplied by the project
  owner) moved to `app/src/main/res/drawable-nodpi/skywire_logo.png` and wired
  everywhere the placeholder mark lived: the bar's center slot (44 dp,
  untinted — the selection pill signals the active state), the splash icon
  (`ic_splash_mark.xml`, 53×40 dp layer-list inside the masked circle), and
  the adaptive launcher icon foreground (white-tinted cloud on the brand-blue
  background layer). The hand-drawn placeholder vector was deleted.
- The Skycoin typeface (Light/Regular/Bold + italics, OTF) moved to
  `app/src/main/res/font/` and applied across the entire Material 3 type scale
  (`ui/theme/Type.kt`). Roles whose Material default weight is Medium (500)
  are pinned to Bold — the family ships no 500 cut, and they would silently
  fall back to Regular and flatten the emphasis hierarchy.
- Designed shared top bar (`ui/components/SkyTopBar.kt`): centered bold title
  in the brand typeface, tonal circular back button, container blending into
  the background; takes an `actions` slot so app screens can add `Logs` etc.
  All pushed routes use it via `AppRouteScaffold`.

**Verified:** rebuilt + reinstalled on DM-B70104; screenshots confirm the
cloud in the splash (light + dark backgrounds), the untinted cloud in the
bar pill, the typeface on every label, and the new top bar on the SkySOCKS
route — light and dark.

---

## 2026-08-03 — Android app skeleton: theme, splash, bottom bar, apps hub

**Built:** the `android/` Gradle project (this module) — Kotlin / Jetpack
Compose / Material 3, single-Activity architecture.

- Gradle 9.6.1 (committed wrapper) · AGP 9.3.1 · Kotlin 2.4.10 · Compose BOM
  2026.06.01; version catalog in `gradle/libs.versions.toml`. Note: AGP ≥9 has
  built-in Kotlin — the standalone `org.jetbrains.kotlin.android` plugin must
  NOT be applied (build fails with a dedicated error if it is).
- `minSdk 26`, `targetSdk 36`, `compileSdk 37`, arm64-v8a only.
  `useLegacyPackaging = true` so the Go payload is extracted at install time —
  required for exec-ing `libskywire-mobile.so` as a child process later.
- Brand theme (`ui/theme/Theme.kt`): primary `#0072FF`, white/black
  backgrounds, near-black dark surfaces, dynamic color off.
- Splash via `androidx.core:core-splashscreen`: Skycoin mark on white (light) /
  black (dark), 250 ms fade into Home.
- Bottom `NavigationBar`, 5 slots: Home · Chat · **Skycoin mark (icon-only,
  enlarged)** · Wallet · Settings. The center slot opens the apps hub.
- Apps hub: adaptive grid of rounded tiles — SkySOCKS, SkyVPN, SkyDEX,
  SkyChat, Wallet, Fleet, plus SkyMeet and Pay-with-Sky greyed as
  "Coming soon" (no click). SkyChat/Wallet tiles route to the same
  destinations as their tabs. SkySOCKS/SkyVPN/SkyDEX/Fleet push full-screen
  routes; back returns to the hub.
- All icons are Material Symbols placeholders behind `Painter` parameters —
  designed logos drop in later with zero layout change. The Skycoin mark
  (`res/drawable/ic_skycoin_mark.xml`) is a geometric placeholder awaiting the
  brand SVG.
- Placeholder `BiometricGate` composable so the navigation structure is final
  before the real app-lock lands.
- Root Makefile bridge targets: `android-apk` (assembleRelease),
  `android-apk-debug` (assembleDebug).

**Verified:** debug APK (44 MB, includes the 63 MB payload compressed) built
and installed on DM-B70104 (arm64, Android 15); splash → themed Home; all five
bar destinations switch; hub tiles push their routes and back returns to the
hub; light and dark checked via screenshots. AVD `skywire`
(android-37.0 google_apis arm64) created for emulator work.

---

## 2026-08-03 — On-device smoke of the Go core (real device)

**Built:** version stamping fix in the root Makefile — the mobile targets now
also stamp the repo-local `pkg/buildinfo` (`MOBILE_APPINFO`), with a
semver-safe fallback version (`v0.0.0-<sha>` when no git tag is reachable),
because `visorconfig.Parse` rejects a non-semver build version at startup.

**Verified on DM-B70104 (arm64-v8a, Android 15), NDK-cgo payload:**

- `config gen` ran on-device: DNS + HTTPS fetch work through bionic (the
  reason the NDK/cgo lane exists).
- Visor connected to dmsg from the phone's network; local API answered on
  `127.0.0.1:8000` through `adb forward`; auth + CSRF enforced.
- skychat, skydex-client, skysocks-client reached **Running in-proc** — one
  process total (~88 MB RSS), every listener loopback-only, no RPC port.
- Real traffic: desktop browsed the web through the phone's SOCKS5 over the
  Skywire mesh (skysocks-client → public proxy server).
- vpn-client start fails on-device with "could not find default network
  gateway" (Linux `ip r` route code) — expected; the Android VPN glue
  (VpnService TUN handoff) is a later part. Failure was contained; the visor
  kept running.
- Sizes recorded: pure-Go 63,242,536 B; NDK-cgo 63,237,504 B (both ≈ 21.2 MB
  gzipped).

---

## 2026-08-03 — `skywire-mobile`: the lite Go core (one binary)

**Built (Go side, commit `ef43096db`):**

- `cmd/skywire-mobile`: one multicall binary — the visor, the CLI `config`
  subtree, and skychat / skysocks-client / vpn-client / skydex-client. Apps
  run in-process via the launcher's internal-app registry; `app <name>` is the
  per-app exec fallback.
- `mobile` build tag strips ~51 MB of embedded desktop assets, each with a
  graceful-absence path: GeoIP DB (30 MB), manager web UI (6.8 MB → "/" serves
  a one-line API-only page), vendored browser wallet (11 MB → /wallet/* 404),
  tpviz legacy UI (2.8 MB), browse handlers.
- API-only surface on mobile: new `hypervisor.dmsg_ingest` config bool gates
  the remote-visor dmsg RPC ingest (off by default; the Fleet feature flips
  it). Desktop behavior unchanged.
- Build/CI: `make build-mobile` / `android-mobile` / `android-mobile-check`
  (80 MB size budget) / `android-mobile-ndk`; `android` CI job in
  `.github/workflows/test.yml`.

**Verified (desktop smoke, macOS host build of the same variant):** all four
apps started in-proc from one binary (three Running; vpn-client blocked only
by macOS route permissions); authenticated API + CSRF; exec fallback spawned a
real child process; Fleet toggle verified off→on; geo lookups degrade to
"??"; RPC port closed; SOCKS5 browsing through a public proxy worked
end-to-end. Binary: 63.2 MB raw / 21.2 MB gz (from 265.6 MB naive five-binary
payload).
