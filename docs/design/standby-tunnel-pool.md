# Standby tunnel pool — design + implementation plan

Written 2026-09-17 against `develop` c4675c6d0, read-only. Supersedes criterion 8's "route ranking
first" decision. User's direction, binding: *"route ranking before dialing isn't the best approach,
just dial / set up the routes and hold them in standby … the only way to have routes that can be
switched in in an instant."* Plus 2026-08-29: the pool is **as many disjoint routes as exist to the
exit**, sized from what is reachable, settling and resting at the real disjoint bound; **control the
active set, never reap the pool**.

---

## A. Current state

### A1 — packet-level (legs inside one route group): the pool exists and is OFF for the shipping proxy

| thing | where | value / behaviour |
|---|---|---|
| pool size tunable | `pkg/router/policy/preset/tick.go:685` `AdaptStandbyMax()`, setter `:722` | atomic, default **512** (`init()` `tick.go:668-672`) |
| active floor / ceiling | `tick.go:676` `AdaptRevActive()`, `:679` `AdaptCap()` | **2** and **8** |
| requested mux width | `pkg/router/policy/preset/preset.go:361` | `Mux = AdaptRevActive() + AdaptStandbyMax()` = **514** for the `adaptive` preset |
| CLI | `cmd/skywire-cli/commands/proxy/mux_ops.go:141` `standby <n>` → `pkg/visor/api_transport.go:199` `SetMuxStandby` → `SetAdaptStandbyMax` | live, per-visor, per-end |
| foreground dial bound | `pkg/router/router_dial.go:2996` `initialForegroundMux = 16` | `establishMuxRoutes` (`:2998`) caps the synchronous fill; the rest is background |
| background fill | `router_dial.go:795-825` (goroutine) → `route_group.go:1413 maybeSelfHeal` | one setup-node dial at a time |
| stopping rule | `route_group.go:1365` `selfHealNoProgressLimit = 4`; log at `:1467` **"no disjoint path available right now; settling at current degree"** | 4 consecutive adds that grow the degree by 0 → stop. Re-armed by a leg death or a new transport |
| live re-cap of the target | `route_group.go:2043-2048` (`rotationServiceFn` → `SelfHealTargeter`) → `route_group.go:1340 setSelfHealTarget` | lowering `standby` at runtime really lowers the target |
| standby flag | `pkg/router/route_mux.go:824 setLegStandby`, `:907 isLegStandby`, `:846 parkAllAuxStandby`, `:808 legSelectableIgnoringStandby` | leg 0 can never be standby; a standby leg is skipped by `selectTransport` but **is** an emergency failover target |
| park hysteresis | `pkg/router/park_hysteresis.go:38` `legParkMinHold = 30s`; `:111 promoteLegAdaptive` | one seam for every adaptive promote; emits `leg_promoted` |
| idle measurement | `route_group.go:43` `legLivenessInterval = 30s` (end-to-end ping/pong), `:56 legDataProgressInterval = 5s` (local counters), `pkg/router/leg_rtt_window.go:27` `legRTTMinWindow = 30s`, `:31` cap 64 samples | **RTT only**; no capacity for a leg carrying nothing |
| keepalive that holds the rules | `route_group.go:30` `defaultRouteGroupKeepAliveInterval = DefaultRouteKeepAlive/2` = **5 min**, loop `:1983`; rules expire at `pkg/routing/table.go:258 CollectGarbage` / `:287 ruleIsTimedOut` against `Rule.KeepAlive()` = `pkg/router/router.go:34 DefaultRouteKeepAlive = 10 min` | an idle held route group costs **one keepalive packet per 5 min per group**, and its rules at every hop never expire |
| cost of a promote | `route_mux.go:824` + `route_group.go sendLegState` | a flag flip + one control frame. **No setup node.** (`docs/warm_standby_legs_rfc.md:18` measures a fresh min-hops-2 leg at **8–9 s**) |

**Is it functional?** The machinery is, and the `legs-N` bench sets exercise it (a5b973a97
`mux-legs-2.legs.json:70` shows a live `"standby": true` leg). **But the shipping proxy never uses
it**: `cmd/apps/skysocks-client/commands/skysocks-client.go:533-537` dials every tunnel with
`mux = 1` — so each tunnel is a **single-leg** group, `muxTarget = 1` at `router_dial.go:704-712`,
`establishMuxRoutes` is a no-op (`:823`) and `maybeSelfHeal` returns at `route_group.go:1418`
(`target <= 1`). Confirmed by the closing run: every group in `mux-tunnels-2.legs.json` has exactly
one leg. The 512 default is therefore **dormant, not dangerous** — the fleet storm of #4325 cannot
fire from the proxy default as it stands.

### A2 — stream-level (tunnels): where the pool must go

| thing | where | behaviour |
|---|---|---|
| default | `pkg/skyenv/skyenv.go:339` `SkysocksClientTunnels = 2` | read at `cmd/apps/skysocks-client/commands/skysocks-client.go:142,180` and `cmd/skywire-cli/commands/proxy/proxy.go:94` |
| dial loop | `skysocks-client.go:378-386` | **sequential**, `diversify=true` for tunnels 2..N; sequential is load-bearing (`:363-377`: tunnel *i* must be registered in `rgsNs` before tunnel *i+1*'s exclusion scan) |
| one tunnel | `skysocks-client.go:514 dialServer` → `pkg/app/client.go:240 DialWithOptions` → `pkg/app/appserver/rpc_ingress_gateway.go:242 DiversifyTransports` → `pkg/router/router.go:395` | |
| sibling exclusion | `router_dial.go:124-140` ← `siblingRouteGroupExclusions` `:3613` | scans `r.rgsNs` for groups to the same `(rPK, rPort)`; returns their first-hop tp IDs, peer PKs, remote IPs. `count==0` ⇒ no exclusions at all (lone dial byte-identical) |
| candidate admission / ranking | `freeFirstHops` `:2100` (→ `filterDisjointFirstHop` `:1984` + `filterDisjointFirstHopPeer` `:2018`); `filterLANFirstHops` `:2068`; `rankFreeFirstHops` `:1911` → `probeFirstHopLatencies` `:1842` (`firstHopProbeTimeout = 2s` `:1819`) → `rankByPathLatency` `:1673`; `ProbeLatency` impl `pkg/transport/managed_transport.go:327` | ranking **orders**, never filters. Invoked from `fetchBestRoutes` `:1427`, `parallel_route_setup.go:327`, `rsn_oracle_routes.go:441` |
| `dial_decision` | const `pkg/router/mux_events.go:82`; trail `DialOptions.note` `pkg/router/router.go:891`; **single emit** `router_dial.go:668` inside `finishDial` `:890` | one per group, only when notes exist |
| dial bound | `dialSetupCeiling = 90s` `pkg/app/appserver/rpc_ingress_gateway.go:32` | per tunnel dial |
| session set | `pkg/skysocks/client.go:79` `sessions []*yamux.Session` + `:90` `recvStamp map[*yamux.Session]*tunnelMeter` (`sessionsMu` `:80`), `:782 snapshotSessions` | `AddTunnel` `:537` appends; **no `RemoveTunnel` exists** — a live tunnel is only ever retired by the keepalive loop |
| picker | `client.go:861 pickSessionFor(pickDir)`; `pickAny` (`:450`) `score := rtts[i] * float64(n+1)` at **`:918`**; `pickRecv` (`:451`) `score := float64(n+1) / cp` at **`:948`** with the stale-idle credit `:944-947`; `pickSession()` `:812` | `sitOut[]` (`:876-889`) already implements "skip this tunnel unless it is the only live one" — the exact shape a standby mask needs |
| per-tunnel measurement | `client.go:277 tunnelMeter`: `rttMs` EWMA (`:308 recordRTT`, α `:305 = 0.25`), `rxCapBps/txCapBps` (`:356 sample`, busy-only — #4965, `if !busy { return }` at `:373`), `:426 capacity` | `meterSampleMin 500ms`, `meterCapDecay 0.9`, `meterFresh 2s` (`:336-340`); `exitOpenPenalty = 10s` (`:404`) |
| where RTT is measured, free, on an idle tunnel | `client.go:1145 sessionKeepAliveLoop`; `tunnelRTTProbeInterval = 5s` (`:1103`, ticker `:1152`, ping `:1196`), plus the liveness ping `livenessProbeInterval = 15s` (`:1097`) at `:1227` | **this is the promoter's home** |
| death detection | same loop `:1236-1250`: no pong **and** no bytes for `sessionHardDeadWindow = 45s` (`:1124`) → `s.Close()`; then `:1262 maybeRedial` `:693` | `maxRedialFails = 3` (`:666`), one in-flight re-dial, `SetTunnelTarget` `:558` |
| per-stream death, already instant | `tunnelGuard` `pkg/skysocks/rangesplit.go:552`, installed at `:686`/`:610`; `errSessionClosed` `:97`; free retry `:499-503` (#4974), `rsFreeRetries = 8` | a **range chunk** on a dead tunnel refetches in one round trip. A **lone stream** (a browser conn, a POST) is not retried at all — it dies with the tunnel |
| app-stop cleanup | `CloseRouteGroupsForApp` `pkg/router/router_rules.go:189`, bound `appRouteGroupCloseTimeout = 5s` `:168`; sole caller `pkg/visor/api_apps.go:471` | app-scoped, all-or-nothing; a standby tunnel carries the same app tag so it is reaped with the rest |

### A3 — what a "standby TUNNEL" is, and the verdict

A standby tunnel = **a dialed route group + noise + yamux session to the exit, with zero user
streams**. Everything needed to hold one already exists and costs almost nothing:

| cost, per held tunnel, per end | value | source |
|---|---|---|
| route rules at every hop | TTL is **per rule**, `DefaultRouteKeepAlive = 10 min` (`router.go:34`), swept every `DefaultRulesGCInterval = 10 s` (`:36`) by `rulesGCLoop` → `CollectGarbage` (`router_gc.go:11`, `table.go:258`). Refreshed by *any* packet: write → `UpdateActivity` (`route_group.go:1258`), inbound → `GetRule` (`router_packet.go:299`). **The 30 s leg-liveness ping, not the 5-min keepalive, is what actually re-arms the whole chain — 20× inside the TTL, from both ends** | |
| wire traffic while idle, per leg per end | Ping 23 B + Pong 23 B / 30 s; LegState 8 B / 7 s (non-primary); KeepAlive 7 B / 5 min (skipped if anything was sent); SACK only on a gap. ≈ **10 B/s for a 3-leg tunnel, both ends** | `packet.go:344,357,506`, `route_group.go:2334,2457,3557` |
| goroutines, per route group | **Measured: 12, identical on both ends** (`pprof/goroutine?debug=2` against 32 live groups) — 7 `serviceKnobLoop` (leg-liveness 30 s, leg-dataprogress 5 s, **send-window 100 ms**, reorder-stall 500 ms, legstate-resync 7 s, unidir-flip 1 s, **tlp 100 ms**), 2 `servicePacketLoop` (keep-alive 5 min, sack 2.5 min), plus `serveIntake`, `read` and `Read`. `fec-flush` and rotation are extra when negotiated/set | |
| timer wakeups while idle, per group per end | **≈ 23/s**, almost all of it send-window + tlp at 10 Hz each. Since the idle-suspend gate (`service_gate.go`) those five loops park when the group is quiet or single-leg, leaving **≈ 0.24/s**; `mux.idle_suspend_grace = 0` restores the unconditional tickers | `service_gate.go` |
| exit-side, per accepted tunnel | the above **+ 1 `yamux.Server` session (2 goroutines) + 1 `socks.Serve` goroutine**. `MaxStreamWindowSize` 16 MiB is a flow-control ceiling, **not** an allocation | `pkg/skysocks/server.go:121-135`, `client.go:46` |
| per-group memory | `readCh` 1024 slots ≈ 24 KB (`route_group.go:31,478`) + ~2.5 KB/leg. `reorderBuf`/`retxBuf` are maps capped at `reorderWindow = 32768` but **empty while idle** (`route_mux.go:420`, `reorder.go:43`) | |
| setup-node work | **once**, at dial: 1 `DialRouteGroup` RPC, then `ReserveIDs` **parallel per visor** and `AddIntermediaryRules` **parallel per intermediary** (`setupnode.go:605,670`); ~8–9 s for a min-hops-2 path. Bounds: `routeSetupDialTimeout` 30 s (`router.go:74`), `rpcDeadline` 30 s, `handshakeAwaitTimeout` 10 s | `docs/warm_standby_legs_rfc.md:18` |
| promotion cost | zero route work — a mask flip in `pickSessionFor` | this plan |
| prior art | `pkg/skyroute/pool.go` is already a **held-open route-group pool** (keyed route groups, yamux-muxed, `DefaultIdleTTL = 10 min`, negative cache) built for the resolving proxy — read it before writing PR 2 | `pkg/skyroute/pool.go:1-63` |

**What an idle tunnel knows:** RTT, yes (yamux ping, every 5 s, already EWMA'd). Capacity, **no** —
`tunnelMeter.sample` is called only from `pickSessionFor:899` with `busy = NumStreams() > 0`, so a
zero-stream tunnel never sets `busyAt` and `capacity()` returns `(0, false)` forever (#4965). That
gap is §B4. There is **no** warm/idle byte-moving probe anywhere in `pkg/skysocks` today
(`pkg/skysocks/server.go` has no keepalive at all, `EnableKeepAlive = false` at `server.go:124`);
`pkg/router/warm_route_pool.go` caches disjoint **plans** (`defaultWarmPlanTTL = 30s`,
`warmPlanBucketCap = 64`), never a dialed group.

### A4 — the five gaps this design must close

| # | gap | evidence |
|---|---|---|
| 1 | no idle capacity signal | `client.go:373` `if !busy { return }` |
| 2 | no way to remove a *live* tunnel | `AddTunnel` `:537` has no inverse; eviction named a deferred follow-up at `:687-692`. **Fine — the rule is never reap the pool** |
| 3 | `target` is one scalar | `:149`, `SetTunnelTarget` `:558`; `maybeRedial` refills purely on `live < target` |
| 4 | a standby tunnel already counts as a sibling | `siblingRouteGroupExclusions` `:3613` keys on `r.rgsNs`, and `saveRouteGroupRules` registers synchronously — **this works in our favour**: each pool dial is automatically steered off every tunnel already held, active or standby, and exhaustion is detectable |
| 5 | teardown granularity is app-wide | `router_rules.go:189` |

---

## B. The minimal design

### B1 — shape

`sessions[0..N)` = active (picked); `sessions[N..P)` = standby (held, pinged, never picked). The app
dials the active N as today, then fills the pool in the background until `ErrNoDisjointFirstHop`; a
promoter in `sessionKeepAliveLoop` swaps one for one. Active-set size stays `--tunnels` (2, the
shipped default, unchanged). The **pool** is new.

### B2 — data structures (all in `pkg/skysocks/client.go` unless noted)

| item | shape | note |
|---|---|---|
| `Client.standby` | `map[*yamux.Session]bool` guarded by `sessionsMu` | mirrors `recvStamp`'s keying; absent = active |
| `Client.activeTarget` | `int` | = `--tunnels`; `SetTunnelTarget` keeps its current meaning (live-tunnel floor) |
| `Client.poolTarget` | `int` | = `min(--standby-pool, disjoint bound discovered at fill time)`; 0 disables |
| `Client.SetPoolDial(fn func() (net.Conn, error))` | mirrors `SetTunnelRedial` (`:635`) | the app owns the dial; the Client owns the pool |
| `Client.poolSettledAt` | `time.Time` | set when the fill stops; re-armed only by a tunnel death or a `tp` change |
| `DialOptions.RequireDisjointFirstHop` | `pkg/router/router.go` next to `DiversifyTransports:395` | new |
| `router.ErrNoDisjointFirstHop` | `pkg/router/router_dial.go` | returned only when `DiversifyTransports && RequireDisjointFirstHop` and every ranked candidate's first hop is already held by a sibling group (the list `router_dial.go:1409-1426` already computes) |

The fill reuses the app's existing **sequential** `diversify=true` dial verbatim
(`skysocks-client.go:378-386`): a standby tunnel is a live route group in `r.rgsNs`, so
`siblingRouteGroupExclusions` (`router_dial.go:3613`) already steers dial *k+1* off every first hop
the first *k* hold — active or standby — and the sequential order is what makes that reliable
(`skysocks-client.go:360-377`). Nothing new is needed to make the pool disjoint.

**Sizing is discovered, not configured.** The app fills the pool one dial at a time; the *first*
`ErrNoDisjointFirstHop` means the topology is exhausted → log `standby pool settled at N tunnels`,
set `poolSettledAt`, **stop**. This is the tunnel-level twin of `route_group.go:1467`. On the rig
that is 6 intermediates + direct stcpr + direct squicr = **up to 8**. `--standby-pool` (default 8 =
`AdaptCap()`) is a ceiling, never a target to chase — the storm of #4325 was caused by chasing 513.

### B3 — the promoter

Lives in `sessionKeepAliveLoop` (`client.go:1145`) as one more `case <-promoteTicker.C`, so it runs
on the goroutine that already holds every tunnel's fresh RTT and capacity.

| constant | value | why |
|---|---|---|
| `tunnelPromoteInterval` | 5 s | the cadence RTT is already refreshed at (`:1103`) |
| `tunnelPromoteMargin` | 1.25 | a standby must beat the worst active by 25 % on the same statistic |
| `tunnelPromoteHold` | 15 s (3 consecutive decisions) | mirrors `adaptHysteresis = 3` (`tick.go:633`) |
| `tunnelParkMinHold` | 30 s | identical to `legParkMinHold` (`park_hysteresis.go:38`); a parked tunnel cannot be re-promoted inside it |
| `standbyProbeInterval` | 30 s | §B4 |
| `standbyProbeBytes` | 256 KiB | §B4 |

Decision rule (one swap per tick, at most):
1. score every tunnel with the **same** statistic the picker uses: `rtt` for `pickAny`-dominated
   idle periods, `capacity` when any tunnel is busy. Never mix.
2. `worstActive` = highest score; `bestStandby` = lowest. Skip benched (`onBench`) and closed ones.
3. swap only if `worstActive >= tunnelPromoteMargin × bestStandby` for `tunnelPromoteHold`, the
   demoted tunnel has **no open streams**, and its park hold has expired.
4. a swap flips two map entries. Open streams are never migrated — a demoted tunnel with streams is
   simply not eligible, so nothing in flight is disturbed.
5. rank on the **yamux ping** (`tunnelMeter.rttMs`), which is a true symmetric end-to-end RTT. Do
   **not** rank tunnels on the router's per-leg `route_latency_ms`: `sendPong` always replies on leg 0
   (`route_group.go:2233`), so that number is *leg-i forward + leg-0 reverse*.

**Criterion 6, "no rebuild":** a dead active tunnel is replaced **instantly**, not by re-dial. In
`sessionKeepAliveLoop`'s retire branch (`:1244-1250`), after `s.Close()`, promote the best standby
into the freed active slot in the same tick; `maybeRedial` (`:693`) then refills the *pool* tail in
the background instead of the *active* set. Today the active width is restored only by a fresh dial
(`dialSetupCeiling` 90 s, ~8–9 s typical, plus up to `sessionHardDeadWindow` 45 s before the death
is even seen). What this does **not** fix: a lone stream (a POST) already spliced onto the cut
tunnel still dies with it — `handleStream` (`:1301`) has no retry and stream migration is out of
scope. Range chunks are already rescued in one round trip by `tunnelGuard`/#4974. So the honest
target is *the next stream lands instantly on a live tunnel and the client never collapses*, not
*the in-flight POST survives*.

### B4 — live measurement of a tunnel carrying nothing

| option | cost | verdict |
|---|---|---|
| yamux ping RTT | already paid, every 5 s | **keep** — the promotion statistic while idle |
| audition: swap a standby in for one pick cycle while **no** tunnel has streams | zero extra bytes | **default.** `pickSessionFor:944-947` already credits a stale idle tunnel the best known capacity "so it gets probed"; the audition just makes a standby *eligible* for that credit |
| explicit burst probe | `--standby-probe-url` (e.g. `http://127.0.0.1:18080/?bytes=262144`, the rig's exit-local sink; `parseByteRange` at `cmd/skywire-cli/commands/proxy/loadtest.go:398` also allows a `Range` form) — one yamux stream on the *named* standby session, SOCKS5 CONNECT + GET, body discarded, bytes counted by the session's own `recvStampConn` so `sample(now, busy=true)` credits it | **bench + opt-in.** One 256 KiB GET per standby tunnel per 30 s |
| exit-advertised probe target (a reserved CONNECT target the skysocks server answers with N bytes) | a wire/app-protocol addition, capability-negotiated | **deferred** — the production answer, not this phase |

Probe safety rails (all three mandatory): **skipped entirely while any active tunnel has open
streams** (`s.NumStreams() > 0`); hard cap `standbyProbeBytes`; at most one probe in flight per
tunnel. Worst case on the rig: 6 standby × 256 KiB × 2/min = 3 MB/min ≈ 50 KB/s against a 9 MB/s
link = **0.55 %**, and zero during any measured row.

### B5 — observability (what the bench reads to prove a switch)

| surface | change |
|---|---|
| `pkg/router/mux_events.go:56-83` | two new kinds: `tunnel_promoted`, `tunnel_parked`; reuse `MuxByAdaptive`/`MuxByOperator`; `Reason` names the statistic and both scores ("capacity 2.1 vs 7.4 MB/s, margin 3.5x") |
| app → ring | one new ingress method `NoteMuxEvent(localPort routing.Port, event, reason string)` on `pkg/app/appserver/rpc_ingress_gateway.go`, resolving `localPort` to the route group whose `desc.dst_port` matches, then `noteMuxEvent`. The app's promoter is the only caller. Keeps the bench's existing reader (`visor state --select diag` → `.diag.mux_events`, `bench/run-mux.sh:138-143`) working unchanged |
| `pkg/visor/rpc.go:317 MuxRouteGroupInfo` | `+ TunnelRole string \`json:"tunnel_role,omitempty"\`` — `"active"` / `"standby"`, set from the app's last report. Lands in `visor state --select mux_route_groups` (`pkg/visor/api_state.go:58`) on **both** ends, which is what criterion 5 needs |
| `pkg/proxystatus/provider.go:262 Tunnel` | `+ Role string`, `+ RTTMs float64`, `+ CapBps float64`, `+ Streams int` — status.skysocks and `proxy tree` then show the pool |
| `cmd/skywire-cli/commands/proxy/mux_info.go` | prints `role` per group; a new `proxy mux pool` prints the pool table (size, settled-at reason, per-tunnel role/RTT/capacity). Note `proxy mux info --json` re-marshals through a CLI-local mirror (`mux_info.go:104-283`) that **drops** `agg_*`, `events`, `ack_delay_ms`, `inflight_bytes`, `window_bytes` — so the bench must keep reading `visor state --select mux_route_groups`, and `tunnel_role` must be added to **both** shapes |

---

## C. PR ladder

Each PR: branch from `upstream/develop` → push to `0magnet` → `gh pr create --repo skycoin/skywire
--base develop --head 0magnet:<branch>`. Smoke before merge: `$S/smoke.sh <sha>` (tunnels-2 +
legs-2, 2 trials) — gate is no regression against `bench/2026-09-16/a8c3b6486` bars.

| # | change | files | criterion | before → after measurement |
|---|---|---|---|---|
| 1 | `RequireDisjointFirstHop` + `ErrNoDisjointFirstHop`; the ranked candidate list already exists, this only names its exhaustion | `pkg/router/router.go`, `router_dial.go`, `parallel_route_setup.go`, `pkg/app/appserver/rpc_ingress_gateway.go`, `pkg/app/client.go` | — (enabler) | unit test only; smoke must be byte-identical (nothing sets the flag yet) |
| 2 | standby mask + picker skip (`standby` map, `pickSessionFor` skips standby exactly as `sitOut` does, with the same "unless nothing else is live" escape); `--standby-pool` flag, background pool fill until `ErrNoDisjointFirstHop`, `poolSettledAt` | `pkg/skysocks/client.go`, `cmd/apps/skysocks-client/…`, `pkg/skyenv/skyenv.go` | 6 (tunnels) | `mux-tunnels-2`: 2 route groups → up to 8; **active set still 2**, so all four cells must stay within noise of a5b973a97 (50 MB down ≥ 7.0). Exit memory before/after, 20 min apart: `cli pty exec <exit pk> -- sh -c 'p=$(systemctl show -p MainPID --value skywire); grep -E "^(VmRSS\|RssAnon)" /proc/$p/status'` |
| 3 | instant failover: on retire, promote the best standby in the same tick; `maybeRedial` refills the pool tail, not the active set | `pkg/skysocks/client.go:1236-1262` | **6** | `bench/run-degrade.sh SUBJECTS=tunnels-2`: today ttfb-after-cut 35–40 s on a download. Target **< 2 s**, `rg_ports_after` shows the surviving groups unchanged (no rebuild), 3/3 download hashes. The in-flight POST is still expected to fail — record it, do not claim it |
| 4 | `tunnel_promoted` / `tunnel_parked` events + `NoteMuxEvent` ingress + `TunnelRole` in `MuxRouteGroupInfo` + `proxystatus.Tunnel` fields | `pkg/router/mux_events.go`, `pkg/app/appserver/*`, `pkg/visor/rpc.go`, `pkg/visor/api_routing.go`, `pkg/proxystatus/provider.go`, `cmd/skywire-cli/commands/proxy/mux_info.go` | 5, 7 | the degrade run of PR 3 re-run: ≥1 `tunnel_promoted` in `.mux_events.json`, and `tunnel_role` present on both ends' `mux_route_groups` |
| 5 | the promoter loop (margin / hold / park hold) on the RTT statistic, idle only | `pkg/skysocks/client.go` | 2, 8 | `mux-standby-8` vs `mux-tunnels-2` on the same commit: 50 MB down median must not fall; churn ≤ 10 `tunnel_*` events per 20-row set (criterion 7's bound) |
| 6 | capacity statistic + audition while idle | `pkg/skysocks/client.go` | 2, 4 | 10 MB down — the weakest cell in every campaign (4.54 vs the 5.00 bar). Target: ≥ bar, i.e. the pool picks the fast route after the bars drift |
| 7 | `--standby-probe-url` burst probe with the three rails | `pkg/skysocks/client.go`, `cmd/apps/skysocks-client/…` | 2, 3 | probe on vs off, same run: medians within noise (proves the rails), and `tunnel_promoted` fires within 60 s of a chaos halt |
| 8 | the packet-level pool under the tunnel pool: raise `mux` from 1 to `AdaptRevActive()` for tunnel groups **only after** 2–7 are green | `cmd/apps/skysocks-client/…:533` | 3, 4 | this is the composition cell (criterion 4, never met: 6.51 vs 7.37/7.05). Do **not** attempt before PR 7; it re-arms the #4325 storm path |

PR 8 is conditional and may be dropped on the numbers. PRs 1–4 are the ones criterion 6 needs.

## D. Bench changes

New `bench/run-standby.sh <exit pk> <out dir> <pins dir> [trials] [sink] [pin order]`, same row
shape and artefacts as `run-mux.sh` so `summarize.sh` / `verdict.sh` work unchanged (set names must
start with `mux-` — `verdict.sh:24`).

| step | detail |
|---|---|
| set name | `mux-standby-<pool>` (e.g. `mux-standby-8`), `POOL` env, default 8 |
| start | `proxy start --tunnels 2 --standby-pool $POOL`; hold the rig lock (`$S/rig.lock`) |
| wait pool full | poll `proxy mux pool --json` (PR 4) until `settled == true` or 180 s; record `pool_size`, `settled_reason`; **INVALID** the set if `pool_size < 4` (the rig offers 8) |
| rows | the standard 20 (5×10 MB down / up, 5×50 MB down / up) |
| chaos | at row 11 (first 50 MB download), halt one intermediate's hop-1 transport with `tp rm <id>` — reusing `run-degrade.sh`'s double fence (must be one of the six pinned stcpr ids, not the exit's, not shared) — then `tp add -t stcpr <pk>` restores the deterministic id at end of set |
| asserts | (a) ≥1 `tunnel_promoted` **or** `leg_promoted` in `.mux_events.json` after the chaos row; (b) rows 11–15 5/5 hashes; (c) the group port set before and after the chaos differs by exactly the killed tunnel — no rebuild of a survivor; (d) `wedges == 0` in both `.recovery.tsv` and `.exit-recovery.tsv` |
| both ends | `EXIT_SNAP=1` as today (criterion 5); `exit_rgs()` jq extended with `tunnel_role` |
| ceiling check | one `mux-standby-8` run with `--standby-pool 0` in the same hour is the control; the bars are a fresh `run-refs.sh` in the same directory (drift is 2x/hour) |

Also: extend `$S/smoke.sh` to a third set `POOL=4 bench/run-standby.sh … ` once PR 4 lands — 2
trials, ~5 extra minutes.

## E. Risks and how each is bounded

| risk | history | bound |
|---|---|---|
| **setup-node dial storm** (8 routes × RSN) | #4325: target 513 was unreachable → endless dials + "Closing conn from untrusted setup node" fleet-wide (`project_selfheal_storm_is_fleetwide`) | the pool is **discovered**, not chased: the fill stops at the first `ErrNoDisjointFirstHop` and sets `poolSettledAt`; re-armed only by a tunnel death or a transport-set change. Fill is sequential (one dial in flight, as today at `skysocks-client.go:378`) and backgrounded, so connect time is unchanged. 8 dials × 8–9 s once per app start, never a loop |
| **exit memory** | the exit is 2c/4G and was OOM-killed 7× on 2026-09-16 (`project_exit_oom_cxds_compaction_2026_09_16`, a cxds-compaction bug, not routing) | 6 extra held route groups ≈ 6 × (24 KB `readCh` + yamux session); the 16 MiB window is a ceiling, not an allocation. PR 2's gate **is** an RssAnon before/after via `pty exec … grep RssAnon /proc/$p/status`; abort above +50 MB for 8 tunnels |
| **exit CPU — the real scaling cost, not memory** | 2 cores; per-thread sampling already peaks at 10.5 % of a core at 10 MB/s | each held group runs **7–10 tickers including 100 ms send-window and 50 ms fec-flush** (`route_group.go:1998`, `fec_flush.go:34`). 6 standby tunnels ≈ +60 timer wakeups/s **doing nothing**. PR 2 must measure exit CPU (`visor state --select diag` `runtime`, and per-thread sampling) with the pool idle for 10 min; if it is not flat, the fix is to make the send-window, fec-flush and tlp loops **skip a group with no active streams** — a cheap early return, not a redesign |
| **keepalive noise read as capacity** | #4965 | already fixed: `tunnelMeter.sample(now, busy)` only learns from a busy window (`client.go:356-385`), and `capacity()` reports `fresh=false` past `meterFresh=2s`. The promoter must use the **same** accessor — never a rate it computes itself |
| **probe disturbing a measured row** | — | the three rails in §B4; the probe is skipped whenever any active tunnel has a stream, which is every row of every bench set |
| **churn / flap** (criterion 7) | #4968: 14 `leg_parked` in 65 s before `legParkMinHold` | `tunnelPromoteMargin 1.25` + `tunnelPromoteHold 15s` + `tunnelParkMinHold 30s`, the same three dampers that ended the leg flap. The bench asserts ≤ 10 `tunnel_*` events per 20-row set |
| **reorder wedge from a wider active set** | the pre-#4337 collapse: the exit sprayed download across standby legs | not reachable here — the active set stays at `--tunnels` 2 and a standby *tunnel* is a separate route group with its own yamux session, so no reorder frontier spans it. (This is precisely why the tunnel level is the safe place to put the pool first, and why PR 8 is last) |
| **a standby tunnel silently black-holes and is promoted into a transfer** | the "silent all-paths stall" open item | promotion requires a *fresh* statistic: a tunnel whose last yamux pong is older than `tunnelRTTProbeInterval × 3` is not eligible, and `exitOpenPenalty` (`client.go:396`) already benches one whose exit open timed out |
| **route volatility making any single measurement wrong** | the 50 MB down bar moved 8.84 → 6.63 → 5.43 in three windows | this plan's whole point; the promoter re-decides every 5 s. Bars must still be re-measured in the same hour as every standby run |

### Not in this phase

dmsg-over-skynet relay legs as a *probe* carrier (`project_mux_followups_standby_pool_and_dmsg_relay_legs`)
— it needs the `ref-dmsg-only` bar first, which is blocked on the `fix/dmsg-socks-client-carries-traffic`
work. The exit-advertised probe target (§B4 row 4) is the production successor to
`--standby-probe-url` and is a capability-negotiated wire change, deliberately out of scope.
