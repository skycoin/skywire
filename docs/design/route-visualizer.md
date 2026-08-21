# Route Visualizer + Route-Selection UI

Status: **design + first-increment scaffold**. This document describes a
ROUTE-oriented view for skywire — distinct from, but modeled on, the existing
network (transport-graph) visualizer — and lands a minimal working slice that
proves the live per-leg data pipeline into a browser.

## 1. Motivation

The network visualizer (`pkg/tpviz`, mounted at `/tp-viz`, and the Angular
`network-visualizer` host that embeds its bundle) renders the **transport
graph**: undirected edges between visor nodes, keyed by transport type. It is a
topology view. It does *not* render **routes** — the actual multi-hop
forward/reverse paths a session uses to reach a destination.

We now have rich per-route, per-leg data that nothing visual consumes:

- The adaptive mux runs **multiple parallel legs** (routes) inside one route
  group, each with its own transport, latency, and live byte/packet counters.
- Legs carry a **gate state**: `alive` (transport up) and `standby` (a warm
  standby — rules kept, not currently sending; see
  `docs/warm_standby_legs_rfc.md`). The routing-policy engine promotes/demotes
  these adaptively.
- Operators already actuate route sets from the CLI
  (`cli proxy mux add/rm/set/grow`, `cli route calc|find`), and the
  routing-policy `ForwardMux`/`ReverseMux` surfaces pick legs by policy.

The goal is a view that, for a chosen destination/app, shows the route group's
legs as paths — per-leg next-hop/intermediate PKs, transport kind, latency,
bandwidth, and active/standby/dead state — updating live, with a seam for a
future route-**selection** UI (pick/pin which route(s) a session uses, exclude
hops).

## 2. Available data (what already exists)

The data plane is essentially complete; the gap is *exposure* and *rendering*.

| Data | Type | Reachable via | Notes |
|------|------|---------------|-------|
| Per-leg live telemetry | `router.MuxInfo` / `visor.MuxRouteGroupInfo` + `MuxLegInfo` | `Visor.RouteGroupMuxInfo(app)` RPC | per-leg tp id/type, next-hop `remote_pk`, `latency_ms`, sent/recv bytes+packets, `retransmits`, `alive`, `standby` |
| Active routes by app | `visor.AppRouteStatus` / `router.RouteStatus` | `Visor.ActiveRoutes()` RPC | per-app; rg bandwidth up/down, `RouteTransport[]` (per-leg fwd/rev rule IDs + latency), `Hops[]` |
| Route groups + hop path | `visor.RouteGroupInfo` + `RouteHopInfo` | `Visor.RouteGroups()` RPC → **HTTP** `/api/visors/{pk}/routegroups` | descriptor, initiator, fwd/consume rule IDs, forward hop path |
| Route candidates | route-finder / local BFS | HTTP `/api/visors/{pk}/route-find`, `/route-calc` | hop lists ready to draw (candidate routes) |
| Routing policy state | `RoutingPoliciesSummary` | HTTP `/api/visors/{pk}/routing-policies` | default + per-app policy |
| Actuation | — | RPC `AddMuxRoute` / `RemoveMuxRoute` / `GrowMuxRoute`; `cli proxy mux add/rm/grow`; policy `ForwardMux`/`ReverseMux` | the seam for route-selection |

Key source files:

- `pkg/router/route_group.go` — `MuxInfo`, `MuxLeg`, `MuxStats()`,
  `RouteHopDetails()`.
- `pkg/router/route_status.go` — `RouteStatus`, `RouteTransport`,
  `ActiveRouteStatuses()`.
- `pkg/visor/api_routing.go` — `RouteGroupMuxInfo`, `ActiveRoutes`,
  `RouteGroups`, `AddMuxRoute`, `GrowMuxRoute`, `RemoveMuxRoute`.
- `pkg/visor/rpc.go` / `api.go` — wire types (`MuxRouteGroupInfo`,
  `MuxLegInfo`, `RouteGroupInfo`, `RouteHopInfo`, `AppRouteStatus`).
- `pkg/visor/hypervisor.go` + `hypervisor_handlers_routes.go` — HTTP surface.
- `cmd/skywire-cli/commands/proxy/mux_info.go` — the CLI's per-leg render +
  byte-delta rate tracker (reference model for the page).

### Two data gaps

1. **No HTTP seam for live per-leg telemetry.** `RouteGroupMuxInfo` and
   `ActiveRoutes` were RPC-only; `/routegroups` gives the static hop path but
   not the live mux legs. → **Closed by this increment** (new
   `/api/visors/{pk}/route-mux`).
2. **No per-leg full path.** `RouteHopDetails()` records only the *primary*
   leg's `forwardHops`; secondary mux legs expose only their **next-hop**
   `remote_pk`, not their full intermediate-PK chain. A true per-leg
   path-over-graph render needs the router to record each leg's hop list.
   This lives in `pkg/router` and is **designed-only / deferred** here.

## 3. Design

### 3.1 The route view

For a selected **(visor, app)** — and, when disambiguation is needed, a
specific route group by src-port:

- **Route-group cards.** One card per active rg: `src:port → dst:port`, mux
  on/off, SACK on/off, leg count.
- **Per-leg rows.** leg index · transport-type badge + short tp id · next-hop
  PK · latency · recv/s · sent/s · **share bar** (this leg's fraction of rg
  throughput) · retransmits · **gate-state pill** (active / warm-standby /
  dead) · cumulative recv/sent. Rates are derived from byte deltas between
  polls, keyed by `rg-descriptor#leg-index` (the model already proven in
  `mux_info.go`).
- **Per-leg mini-path.** `SRC ──(tp)──▶ next-hop ┄┄▶ DST`. Upgrades to a full
  intermediate-PK chain once gap #2 is closed.

**Full feature (designed):** a route-centric *graph* layout — the rg's legs
drawn as coloured paths over the node graph (active solid, standby dashed,
dead greyed), reusing tpviz's node-positioning. Candidate routes from
`/route-find` and `/route-calc` overlay as selectable ghost paths. Live
recolour from the same poll.

### 3.2 The route-selection seam (designed)

The view is the read side of a select/actuate loop whose write side already
exists:

- **Pin / add a leg:** `AddMuxRoute(app, fwd, rev, srcPort)` — hop lists have
  the exact shape `cli route calc --json` emits. UI: pick a candidate path
  from the `/route-find|/route-calc` overlay → POST it as a new leg.
- **Grow for redundancy:** `GrowMuxRoute(app, target, minHops, srcPort)`.
- **Drop / exclude a leg or hop:** `RemoveMuxRoute(app, tpID, srcPort)`;
  hop-exclusion is expressed to the route planner as a constraint (future).
- **Policy-level pin:** the routing-policy `ForwardMux`/`ReverseMux` primitives
  express "prefer/require these legs" declaratively; the UI can emit a policy
  rather than one-shot mux edits. (Policy authoring is owned by another effort
  — this view only *reads* policy state and offers the imperative mux edits.)

New HTTP writes needed for the full selection UI (thin wrappers over existing
RPC, mirroring `postMinHops`): `POST /api/visors/{pk}/route-mux` (add leg),
`DELETE …/route-mux/{tpID}` (drop leg), `POST …/route-mux/grow`. Not in this
increment.

### 3.3 Where it belongs

| Option | Fit | Cost |
|--------|-----|------|
| **Angular HV UI** (`routing.component.ts` already models `groups/find/calc/policies`) | Natural long-term home; where routes are managed and where selection/actuation belongs | Heavy: `make build-ui` (minutes) + committed bundle gated by `make check-ui`; eager (non-lazy) modules |
| **tpviz bundle** (`pkg/tpviz/ui`) | Owns node-graph rendering → best for the path-over-graph layout | Large TS/esbuild SPA; separate committed bundle |
| **Standalone visor-served page** (`/route-viz`) | Zero build step, self-contained, instantly iterable | Not integrated into the SPA chrome |
| wasm-visor UI | Browser-native visor context | wasm build/size overhead |

**Recommendation.** Land the **standalone `/route-viz` page** first (this
increment) to prove the pipeline with no build cost, then graduate the
route-**selection** actuation + the path-over-graph layout into the **Angular
`routing` page** (its `RoutingView` already has a slot; add a `'mux'` /
`'routeviz'` view and reuse `route.service.ts`), embedding the tpviz bundle for
the graph exactly as `network-visualizer.component.ts` does. The standalone
page remains the lightweight/headless diagnostic surface.

## 4. First increment (this PR) — what is scaffolded vs designed

**Scaffolded (working):**

- `GET /api/visors/{pk}/route-mux?app=<name>` — new hypervisor HTTP handler
  (`getRouteMux`, `pkg/visor/hypervisor_handlers_routes.go`) wrapping
  `RouteGroupMuxInfo`. First browser-reachable seam for live per-leg route
  telemetry. Read-only.
- `/route-viz` — self-contained embedded page (`pkg/visor/routeviz/routeviz.html`,
  served by `getRouteViz`, `pkg/visor/routeviz_embed.go`). Visor + app pickers,
  live poll, per-leg rows with rate/share/gate-state, per-leg mini-path. No
  Angular/tpviz rebuild.

**Designed-only (deferred):**

- Route-over-graph layout (needs tpviz node positioning + gap #2).
- Per-leg full intermediate-PK path (needs `pkg/router` to record each leg's
  hop list — out of scope; that package is owned elsewhere).
- Route-**selection** write actions (add/drop/grow legs, hop exclusion) and
  their HTTP wrappers.
- Candidate-route overlay from `/route-find` + `/route-calc`.
- Promotion into the Angular `routing` page.

## 5. Try it

```
# on a hypervisor/visor with the manager UI:
#   open  http://<hv-host>:8000/route-viz
#   pick a visor + app (default skysocks-client), watch legs update live
# raw data:
curl -s http://<hv>/api/visors/<pk>/route-mux?app=skysocks-client | jq
# CLI equivalent (the render model this page mirrors):
skywire-cli proxy mux info -n skysocks-client --watch 1s
```
