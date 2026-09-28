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

These are **host** numbers for a process of its own, not the iOS in-app or
extension footprint (M1 measures `phys_footprint` on the Simulator, D3 the
extension on a device). They are ~3× the ~50 MB packet-tunnel budget the
proposal (§5.2) names as the top risk: the memory levers there
(`debug.SetMemoryLimit`, fewer sessions, lazy listeners) are needed before
Lane D, whatever M1 measures.

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
