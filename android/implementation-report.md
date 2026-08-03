# Implementation report — Skywire Android

Running log of what was implemented, when, and how it was verified.
After finishing a part, add a dated entry at the top: what was built, key
decisions/deviations discovered while building it, and the verification that
was actually performed (commands, devices, measured numbers — not intentions).

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
