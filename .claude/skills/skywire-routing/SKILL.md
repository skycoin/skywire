---
name: skywire-routing
description: Expert reference for skywire routing, mux (multipath route groups), the WASM/Starlark routing-policy presets (route policy, proxy --routing-policy, runtime app policy), per-leg telemetry (proxy mux info --ndjson), mux-bw controlled bandwidth tests, rg/route inspection, and the live routing-policy measurement rig. Load for anything touching routes, mux legs, routing policies, or throughput measurement.
---

# skywire routing & mux expertise

## Inspect routes
- `skywire cli rg ls`, active route groups: APP, REMOTE pk, PORTS `local:remote`, LATENCY, TX/RX, ROUTES count, MUX yes/no; per-leg lines show transport type + latency + route id. (A pty-exec route shows `<pk>:22`; a skynet forward shows `<pk>:57` or `:59`.)
- `skywire cli route ls` (or bare `route`, `route --rid <id>`), installed routing rules. `route calc <src> <dst> --min N` (hop-bound flag is **`--min`/`-n`**; **`--min-hops`** is a hidden alias) plans routes locally; `route find` queries the route-finder. See the **skywire-route-cli** skill for the full route/rg command surface.
- Visor route-setup logs: `skywire cli visor log | grep -iE "cascade|Found routes|Requesting new routes|RouteGroup"`. "Source-driven cascade" = the modern verified route setup; "cascade ACK timeout → falling back to DMSG DialRouteGroup" = setup flakiness; `dmsg error 306 - no associated listener` = the far end has no listener on the dialed routing port.

## Routing-policy presets (WASM + Starlark)
- List: `skywire cli route policy list` prints every preset with a description. WASM presets include adaptive (the composite default), app-mux, elastic-mux, latency-adaptive, probe-and-prune, rotating-bw, coupled, ledbat, geo-avoid, time-of-day, transport-diverse and trust-tiered. Starlark presets include balanced, low-latency-direct, privacy-max, spread-bw, asymmetric-fanout, hypervisor-priority and prefer-connected. The list is the source of truth.
- Preview a policy's decision offline: `route policy test`. Source: `route policy show <name>`. Bench per-call time: `route policy bench`.
- Apply to an app: config `"routing": {"policy_per_dial": "preset:<name>"}`, OR at start: `proxy start --routing-policy preset:<name>` / `skynet start --routing-policy preset:<name>`. Runtime, no restart: `visor app arg routing-policy <app> preset:<name>` (or clear with `none`). Selecting a preset needs no restart; to RE-apply/change, stop/start the app is fine.
- The visor-global `mux_routes` setting is gone. The route count is an output of the routing policy, and `skynet start --routes N --min-hops K` sets it for one forward.

## Per-leg telemetry harness (gate-2)
- `proxy mux info --name <n> --json`, one snapshot of the RG's legs {route_index, intermediate_pk, transport_kind, sent/recv_bytes, latency_ms, alive, standby(gate_state)}.
- `proxy mux info --name <n> --ndjson <file>`, ≥1Hz stream with inst_send/recv_bps + gate_state + leg-lifecycle events (established/promoted/demoted/dropped/failed) on one clock, the same `snapshotLegs()` the policy on_tick ABI reads.
- GOTCHA: a snapshot occasionally catches the RG mid-reconfig reading as 1 leg (partial) → in analysis, drop samples with len(legs)<mux-target and debounce rotation detection ≥2 samples.
- `proxy mux auto <fastest|resilient|…>` is the built-in latency control loop (prune slowest / grow), NOT the WASM presets.

## mux-bw, controlled point-to-point bandwidth (in-process, no external sink)
`skywire cli visor ping mux-bw <pk> --routes N --min-hops 2 --duration 30s [--probe-rtt --idle-baseline 8s --sample-interval 1s --output x.ndjson]`. NDJSON: route_established (per route, with hops), sample (per-leg inst_send/recv_bps + aggregate), rtt_probe, done (avg/peak, queueing Δ). Proves mux>1 beats single route; it dials the router in-process (works even when app-path forwards fail). It plans its OWN disjoint routes (`muxBwPlanDisjointRoutes`, ranks by transport cost #3990), NOT the WASM preset.

## "mux" has THREE meanings, don't conflate
1. **yamux stream-mux over one warm route group**, the resolver/`skyroute.Pool` (port 59). Reuse across short conns.
2. **N parallel router routes**, `--routes` via `DialWithOptions` (skynet-client, vpn-client). Router-level fan-out, no yamux reuse.
3. **N disjoint planned routes**, mux-bw's `routeFinder` planner.

## Live routing-policy measurement rig (gate-3/4)
Goal: steady load over a POLICY-managed mux where the mux route is the bottleneck (not exit egress).
- Sink: `proxy loadtest serve --addr :19999` on a visor you control. Keep it alive with `systemd-run --collect` and open the port in the firewall for the test only. Baseline it: `curl -x socks5h://127.0.0.1:4445 http://<pk>.skynet:19999/` (resolver) or clearnet `curl http://<pubip>:19999/`.
- Driver: a **non-sink-host** skysocks exit running `--routing-policy preset:rotating-bw` pulling the sink's clearnet IP:19999, OR (cleaner, once the skynet-client mux-forward fix lands) a direct skynet forward to the sink. skysocks blocks an exit dialing its OWN ip (self-dial guard) → exit host ≠ sink host.
- Record `proxy mux info --ndjson` during the run and chart per leg bandwidth, rotations and disjointness.
