# Gate G0 evidence — M0 lite core (author's side)

Collected by the author on 2026-09-28, on the dev Mac (Xcode 27.0 27A266a,
Go 1.26.4 darwin/arm64) against `feat/skywire-mobile-ios` on top of
`develop` @ `77300cd95`. The gate record itself (`ios/gates/G0.md`) is the
reviewer's to write.

| File | What it is |
|---|---|
| `deps-before.txt`, `deps-before.err` | `GOOS=ios GOARCH=arm64 CGO_ENABLED=1 go list -deps -tags mobile,withoutsystray,nomsgpack ./cmd/skywire-mobile` before the cuts: 979 packages, 11 of them gopsutil / go-m1cpu / 0magnet/metrics, and `go list` itself fails on gopsutil's `iostat_darwin.c` |
| `deps-after.txt` | the same command after the cuts: 945 packages, none of the three |
| `deps-after-core.txt` | the same for `./cmd/skywire-mobile-core` (the iOS c-archive entry): 927 packages, none of the three |
| `phone-profile.json` | the phone-profile config the measurements ran on, secrets stripped |
| `nil-safety-audit.md` | every reader of the state the 27 dropped modules would have set (item 0.2) |
| `fatal-audit.md` | the §1 fatal list and every other `os.Exit`/`Fatal` in the iOS import graph (item 0.6a) |

## The three answers item 0.1 asked for

1. **Does `GET …/apps/{app}/stats` read the telemetry store?** No:
   `hypervisor_handlers_apps.go getAppStats` → `Visor.GetAppStats`
   (`api_apps.go`) → `v.procM.Stats`, the launcher's process manager. So
   `statsMod` is dropped (§6 D-5). What stats did on the phone besides
   telemetry — the CXO transport-list publisher — falls back to TPD's HTTP
   registration, the path `stats.disabled` visors already take.
2. **Does `/api/svc-fetch` need `dmsgHTTP`?** No: `getSvcFetch` →
   `fetchServiceDataCtx` → `dmsgHTTPCtx` (`api_dmsg.go`), which uses the
   main dmsg client `v.dmsgC`. `dmsgHTTP` stays anyway (the AR, SD and TPD
   clients are dmsg-routed).
3. **`tc` and `tpdco`.** `tc` does not exit the visor (it retries every
   5 min with a dmsg self-transport) — dropped, battery. `tpdco` kept: with
   stats off it is the only TPD cleanup (§6 D-4).

## Numbers (baseline for later gates)

Sizes are from the Makefile targets, both sides built the same way (the
"before" side from `77300cd95` in a scratch worktree):

| Artifact | Before (`77300cd95`) | After (this tree) | Change |
|---|---|---|---|
| `make build-mobile` (host darwin/arm64, not stripped) | 88,443,346 B | 84,943,170 B | −3.50 MB (−4.0 %) |
| `make android-mobile-check` (`libskywire-mobile.so`) | 70,451,496 B | 67,698,984 B (64.6 MiB) | −2.75 MB (−3.9 %) |
| `make ios-core`, each slice (`libskywire-core.a`) | — (did not build) | 77,555,168 B | — |

Host RSS (`ps -o rss=`, KB) of `skywire-mobile visor -c phone-profile.json`,
the two cores running side by side on the same Mac (own configs, own ports),
both answering `/api/ping` for the whole run:

| | at 2 min | at 10 min |
|---|---|---|
| lite core (after) | 105,296 | 140,192 |
| full core (before) | 111,984 | 160,512 |

These are **host RSS** numbers, and RSS is the wrong metric for the
extension budget. It counts the binary's clean code pages (~24 MB of
`__TEXT`, which iOS does not charge) and heap that Go has already released.
What jetsam charges is `phys_footprint`, which macOS reports with
`footprint -p <pid>`. A first version of this README compared the RSS above
with the ~50 MB packet-tunnel budget and called it ~3× over. That comparison
was wrong.

**Re-measured 2026-09-29** at `3d6a5e669` (this tree with develop merged; the
Android payload has the same byte count). Two lite cores ran side by side on
this `phone-profile.json` with fresh identities, idle but connected, and
`/api/ping` answered throughout. Both ran
`GODEBUG=gctrace=1 skywire-mobile visor -c <config> --pprofmode http`, with
`footprint -p` and `ps -o rss=` every 30 s for 10 min and `/debug/pprof/heap`
at the end:

| | RSS (KB) | `phys_footprint` |
|---|---|---|
| lite core as the phone runs it (no Go memory limit) | 94,096–98,800 | 43–62 MB (peak ~1 min after start; 51–53 MB at 10 min) |
| lite core, `GOMEMLIMIT=40MiB` | 84,416–85,920 | 42–45 MB throughout |

At 10 min the live heap after GC is 18–19 MB. Go's default (GOGC=100) lets the
heap grow to a ~37 MB goal before it collects, and that headroom is what a
memory limit takes away: 40 GCs in 10 min instead of 18, each under 5 ms.
About 12 MB does not move: the binary's dirty data segments (~8.5 MB), GC
metadata (~4.7 MB) and ~156 goroutine stacks (~1.7 MB). The largest
live-heap holders are the router's TPD snapshot (~3.7 MB), skychat's SSE hub
(~2.5 MB) and the CXO subscriber walk (~1.6 MB).

So the lite core sits at the ~50 MB budget the proposal (§5.2) names, not 3×
over it. With a memory limit it fits when idle. It still falls short of the
20 % margin D3 asks for (≤ 40 MB), and nothing here measures VPN traffic, the
extension's own Swift/NetworkExtension overhead, or a device. The existing
`memory_limit` setting cannot apply that limit on iOS. `"auto"` reads
`/proc/meminfo`, which iOS and macOS do not have, so the core logs "Could not
detect available memory" and sets no limit. `memlimit.go` also raises any
value below 64 MiB to 64 MiB. Playbook item 1.7 (M1) fixes both.

The size budget stays at 70 MiB. The payload grew 1.65 MB in the five days
before this change, so a budget tight enough to catch the cut regrowing would
break unrelated PRs within a week. What guards the cut instead is
`make ios-deps-check`, now run by `android-mobile-check` in every PR's
Android lane: it fails the moment gopsutil, go-m1cpu or 0magnet/metrics
re-enters the iOS graph.

## Tests (author's runs)

- `make mobile-test` — `pkg/mobilecore` under the mobile tags, `-count=3`:
  `TestStartStopStart` (3 start/stop cycles: `/api/ping` answers, then after
  each Stop the API and skychat ports refuse, all 5 bbolt files reopen,
  process-logger hook count unchanged, goroutines back within margin, no
  stale appnet networker), `TestStopWhileStarting` (Stop 200 ms into a start
  returns in ~1.7 s, no listener left, next start clean), `TestRestartRoute`
  (the API's restart comes back in the same process), `TestStartErrors`,
  `TestGenConfigMatchesTheCommand`, `TestGenConfigErrors`.
- `pkg/transport` `TestManagerCloseAfterServe` (new; fails before the fix).
- Untagged regression run of every touched package (`pkg/visor/...`,
  `pkg/transport/...`, `pkg/skychat/call/...`, `pkg/app/appnet/...`,
  `pkg/logging/...`, `pkg/deployment/tpd/...`, `pkg/router/setupmetrics/...`,
  `pkg/dmsg/dmsg/metrics/...`, `cmd/skywire-cli/commands/config/...`,
  `pkg/mobilecore/...`): all ok.

The first `TestStartStopStart` runs found leaks the exec model had hidden
(Android kills the process, so none of this was ever released there); each
is fixed at its source:

1. `users.db` was never closed — `initHypervisor`'s close entry only
   disabled the hypervisor (`init_apps.go`).
2. `transport.Manager.Close` never returned: `Serve` added 7 to its wait
   group for 6 goroutines (#4633 dropped one), the lock watchdog only watched
   the serve context, and Close waited while holding `tm.mx` — every
   `WalkTransports` caller wedged behind it (`manager.go`,
   `lock_watchdog.go`). This is also why every desktop shutdown logged
   "transport.manager: Module timed out".
3. `tpdco`'s ticker goroutine ranged over a stopped ticker forever.
4. The skynet-forward and app-direct VStream muxes were never closed.
5. `goServeSkynetMirror` listeners never closed (dmsg ping's mirror had no
   closer at all).
6. The voice signaler dropped a skynet listener that joined before `Serve`
   ran, and never closed one that joined after it stopped.
7. appnet's process-wide networker registry kept the stopped visor's
   networkers, so after an in-process restart the modules that start before
   the new launcher bound their skynet listeners to the old, closed router.

## Android emulator loop on the lite payload (Pixel_8a, arm64, API 37)

`make android-mobile-check` payload in a debug APK (`./gradlew
:app:assembleDebug`), installed over the existing 0.0.4 install
(`adb install -r`: the upgrade path, same identity `026958f0…79e5ef8`).

| Step | Result |
|---|---|
| Connect | Connected in ~40 s. The core's log shows the lite set: the previous (full) start ran `ar_bind_cxo, dmsg_server_latency, dmsghttp_logserver, mesh_proxy, node_health, registration_cxo, sd_reg_cxo, skymail, stats`; this start none of them. skychat autostarted. |
| SkySOCKS | Server list loaded (887 servers, `svc-fetch` over dmsg). GB server `03318b3496…f555fd01` connected (latency 2.4 s). From the Mac through `adb forward tcp:11080 tcp:1080`: `curl --socks5-hostname 127.0.0.1:11080 https://api.ipify.org` → `136.244.78.62`; the Mac's own address is `45.56.79.245`. |
| Chat with a desktop peer | Peer: this tree's host build on the Mac (`03d4cad4…60579321`, skychat on 127.0.0.1:18001). Peer → phone over dmsg: `POST /message` acked in 1.9 s, shown on the phone with an unread badge; the peer shows online with its dmsg ping RTT. Phone → peer: sent from the phone's chat UI, peer `/unread` went 0 → 1. (A skynet send cannot route here: neither side autoconnects to public visors, so there is no transport between them.) |
| Fleet on / off | On: "Restart core" → the core restarts, logs `Serving hypervisor RPC over DMSG addr=…:46`, `hypervisor.dmsg_ingest` = true, the Fleet list answers ("Visors (0)"). Off: restarts again, no dmsg RPC listener, `dmsg_ingest` = false. |
