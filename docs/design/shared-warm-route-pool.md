# Shared warm-route pool for multiplexed routing

Status: design (phase-1 prototype landed: shared route-plan cache)

## Problem

A multiplexed route group (a `--tunnels` tunnel, or any mux'd session) keeps a
pool of warm-standby aux legs so it can grow bandwidth onto them without a
cold-dial stall. Today that pool is **per route group**. Each group:

- targets `Mux = AdaptRevActive() + AdaptStandbyMax()` warm legs
  (`preset.go:345`; default `1 + 512 = 513`), and
- runs its **own** self-heal / rotation loop that dials aux legs one at a time
  toward that target (`route_group.go:1149 maybeSelfHeal`,
  `router_dial.go:697 applyAdd` → `addOneAuxLeg`).

With **N** tunnels the costs multiply:

1. **N × the route-finder work.** Every group re-discovers its own disjoint
   aux-leg set: `establishMuxRoutes` and `addOneAuxLeg` each call
   `fetchBestRoutes` (route-finder round-trip + local disjointness BFS /
   `validMuxLeg`) **once per leg, per group, per rotation tick**. N tunnels to
   the same exit run that identical planning work N times over — and the
   route-finder is the slow, timeout-prone part (a cold dmsg session to the RF
   stalls tens of seconds; `router_dial.go:2287` calls it "the observed storm").
2. **N × the setup-node dial storm.** Each warm leg is a full setup-node
   handshake that reserves route IDs and installs rules at every hop
   (`addOneAuxLeg` → `RouteGroupDialer.Dial`, `router_dial.go:2705`). At scale
   these fail and self-heal re-dials, hammering the setup node
   (`tick.go:586-595`).

The user's question: **shouldn't the warm standby be a SHARED pool that any
route group can promote a leg from, instead of each group rebuilding its own?**

This RFC answers *what can actually be shared*, given that routing **rules are
installed per route group**, and proposes a staged path whose first step is
already prototyped.

## What a "leg" is made of — the three layers

A mux leg is three stacked things. Only the bottom two are shareable.

| Layer | What it is | Keyed by | Shareable across groups? |
|---|---|---|---|
| **Transport** | first-hop link `src → intermediate` | `MakeTransportID(src, peer, type)` (`transport.go:19`) | **Already shared** — one `*ManagedTransport` per (peer,type) in `Manager.tps` (`manager.go:81`); groups hold pointers, `SaveTransport` is idempotent (`manager.go:1348`). |
| **Route plan** | the `[]routing.Hop` path to the exit (`{From,To,TpID}`, **no route IDs**) | `(dst, min-hops)` — independent of source port | **Yes** — the path is group-independent; any group can feed it into its own `Dial`. **Not shared today.** |
| **Route rules** | the reserved route-ID chain: source `ForwardRule`+`ConsumeRule` (carry the descriptor) and the `IntermediaryForwardRule`s at every hop | route IDs reserved **per dial** by `IDReserver.PopID`; the edge rules embed the full `RouteDescriptor` (src/dst PK **and ports**) | **No** — see below. |

### Why the rules can't be shared (the crux)

`GenerateRules` (`setupnode.go:609`) mints a **fresh route ID at every hop** for
each dial. The intermediate rules (`IntermediaryForwardRule(keyRID, nextRID,
tpID)`, `rule.go:443`) carry no descriptor — they are pure `routeID → (routeID,
transport)` switches — **but** the chain of reserved IDs terminates at exactly
one `ConsumeRule`, whose descriptor selects one route group at the source
(`router_packet.go:99`: `desc := rule.RouteDescriptor()` → `r.rgsNs[desc]`).

So an intermediate chain is hard-bound to **one** descriptor / **one** route
group. Two tunnels have different descriptors (different source ports at
minimum) and each needs inbound packets to land on **its own** consume rule.
Handing group B a rule chain that group A reserved would deliver B's return
traffic to A. To genuinely share intermediate rules, multiple groups would have
to multiplex onto one shared reserved route-ID chain terminating in one consume
rule — which the source-side descriptor dispatch does not support. **That is a
large refactor, not an incremental change** (see Phase 3).

**Conclusion on the shared unit:** the shareable substrate is the **transport**
(already shared) and the **route plan** (shareable, not yet shared). The
route-ID-bearing rules must still be minted per group by a `Dial`. Sharing
therefore saves *planning* and *transport establishment*, **not** rule
reservation/installation.

## Feasibility verdict

- **True warm-*route* pooling** — promoting a fully-built leg (transports **+
  intermediate rules to the exit**) from a visor-level pool into an arbitrary
  route group — is a **large refactor**. It requires reworking route-ID dispatch
  so one intermediate chain can fan out to multiple descriptors (a shared
  "trunk" abstraction), plus a wire change at the exit. Not incremental.
- **Shared warm-*transport* reserve + shared *plan* cache** is a **tractable
  incremental change**. Transports are already deduped by the manager; the plan
  cache is a small local read-side component. Together they remove the N×
  route-finder storm and bound warm-transport establishment, delivering most of
  the felt cost reduction without touching the rule model or the wire.

### Smallest useful first step (prototyped)

A **visor-level shared route-plan cache** (`pkg/router/warm_route_pool.go`),
consulted by the per-leg dial path before it calls the route-finder. It caches
the disjoint `[]Hop` plans discovered for an exit and lends them to any group
(or later leg, or later tick) dialing an aux leg to that same exit. This is the
single highest-value, lowest-risk slice: every group's ongoing self-heal /
rotation funnels through `addOneAuxLeg`, so wiring that one function makes the
planning work **shared across all groups** while each group still mints its own
rules.

## Model

### Phase 1 — shared route-plan cache (prototyped)

`warmRoutePool` is one instance on the router, alongside the existing
`tpdCache`/`suspects` caches (`router.go`). A cached **plan** is a forward +
reverse `[]routing.Hop` pair (path only, no route IDs) plus its first-hop
transport ID and intermediate PK set (for disjointness matching).

- `put(dst, minHops, fwd, rev)` — records a freshly-discovered plan, dedup by
  first-hop transport, TTL-stamped. Bounded per-exit (`warmPlanBucketCap`).
- `bestPlan(dst, minHops, excludeTps, excludePKs)` — returns a non-stale cached
  plan whose first-hop transport and intermediates are disjoint from the
  caller's per-group exclude sets, else a miss.
- `invalidate(dst)` / `invalidateAll()` — drop plans when transports change.

Wiring (`router_dial.go addOneAuxLeg`): before `fetchBestRoutes`, try
`bestPlan`; on a hit, use the cached plan; on a miss, fetch as today and `put`
the result. **Safety by construction:** the cached plan flows through the
*exact same* downstream gates a fresh plan does — the `validMuxLeg` invariant
check, the "first hop already in the group" reject, and the setup-node `Dial`
that re-resolves and re-dials the transport. A stale plan (a since-dead
transport) is rejected or fails the dial and the caller falls through to a fresh
fetch. **A miss or a stale hit can only cost a fallback; it can never change the
leg that gets built.**

Key includes `minHops` so a `min_hops>=2` dial is never served a 1-hop plan.
TTL (`defaultWarmPlanTTL = 30s`) bounds staleness under the already-cached
5-minute TPD snapshot; expired buckets are evicted on read.

**Why no capability negotiation / flag gate here.** Phase 1 is entirely
initiator-local: it changes *how this visor plans a path*, not anything on the
wire. A peer cannot observe it and is unaffected, so there is nothing to
negotiate and no flag to gate — it is always on, degrading to today's behavior
on every miss. (This honors the "no flag gate" rule by making the optimization
transparent rather than switch-selected.)

### Phase 2 — shared warm-transport pin

Transports are already shared, but a warm transport with **no rule referencing
it** currently reads as tear-down-able: the manager's `RouteChecker`
(`init_router.go:336`) answers "in use?" by scanning the routing table for a
rule whose `NextTransportID` matches. To keep a pre-warmed first-hop transport
alive between the moment the plan is cached and the moment a group dials it,
add a **warm-pin set** the `RouteChecker` also honors:

```
in-use(tpID) := routeTableHasTransport(tpID) OR warmPinned(tpID)
```

The plan cache pins the first-hop transports of the plans it holds and unpins on
eviction. A single background maintainer (keyed by the set of exits the visor
currently tunnels to) calls `EnsureBestTransport` (`manager.go:981`) for each
cached plan's first hop, sized to the topology's disjoint-route count, filled
once — **killing the N× transport-establishment cost** and giving every group's
setup-node dial an already-established first hop. Still initiator-local; still no
wire change.

### Phase 3 — shared route-ID trunk (large refactor; capability-negotiated)

The only way to stop re-minting *intermediate rules* per group is a shared
**trunk**: one reserved route-ID chain from this visor to an exit that multiple
route groups' legs ride, with a demux at the exit that maps the shared inbound
chain back to per-group consume rules. This changes what the exit must do with
an inbound chain, so it **is** a wire change and **must** be capability
negotiated — following the established handshake-bitmap pattern
(`packet.go:174` cap bits, advertised unconditionally at `route_group.go:3176`,
activated only on `local & remote` intersection). Reserve the next free bit
(`CapWarmTrunk = 1 << 8`); a peer lacking it simply never negotiates a trunk and
keeps today's per-group chains. This phase is out of scope for the incremental
work and is recorded here as the end-state, not a commitment.

## How promotion / demotion interacts with the existing machinery

Phase 1 needs **no** change to the tick, the per-group standby arrays, or the
self-heal loop — it only changes where a leg's *plan* comes from. The adaptive
tick (`pkg/router/policy/preset/tick.go`) keeps owning each group's `standby[]`
and its promote/demote decisions; `maybeSelfHeal` keeps restoring degree via
`applyAdd`. The cache sits *under* `applyAdd`, transparently.

Phases 2–3 would move the standby *budget* from per-group to a shared
accountant: the tick's `standbyCount < adaptStandbyMax` gates
(`tick.go:1183/1251/1340/1357`) would request/release from the shared pool
instead of each group owning a full `adaptStandbyMax` reserve, and
`AdaptStandbyMax()` would become a **global pool ceiling** rather than a
per-group target. Those changes belong in `tick.go`/the preset and are
deliberately **not** part of the prototype (that file is under concurrent
edit); this RFC specifies them as the phase-2 interface.

## Sizing, maintenance, direction/disjointness

- **Sizing.** One shared reserve per exit, sized to the topology's disjoint-route
  count (what the self-heal no-progress backoff already discovers,
  `route_group.go:1101 selfHealNoProgressLimit`), filled once — not `N ×
  adaptStandbyMax`.
- **Maintenance.** TTL + `invalidate(dst)` on transport register/deregister; the
  phase-2 pin keeps first hops warm; `EnsureBestTransport` re-dials a dropped
  warm transport.
- **Direction / disjointness.** The cache stores each plan's first-hop transport
  and intermediate set and matches them against the caller's exclude sets, so a
  group growing its k-th first-hop-**disjoint** leg is handed a plan disjoint
  from its own k−1 prior legs — the same guarantee `establishMuxRoutes` /
  `GrowMuxRoute` enforce, preserved on the cached path by the unchanged
  downstream `validMuxLeg` gate. This dovetails with the `--tunnels`
  disjoint-first-hop work: the shared plan set *is* the disjoint set to the exit,
  computed once.

## Bottleneck-relative disjointness and logical-route multiplexing (governing model)

The disjointness the pool must enforce is **relative to the binding bottleneck,
not the transport**. Two legs "conflict" only when they contend for the same
*bottleneck* resource; sharing a transport is a conflict only when that
transport's physical link is the bottleneck. This reframes both the sizing above
and Phase 3.

- **A pool entry is a `(transport, route_id)` logical route, not a transport.** One
  transport already carries many independent routes — an intermediate forwards by
  `route_id`, so distinct route-ID chains over the same hop are independent routes,
  demuxed at each hop. That is why there can be **more disjoint-enough routes than
  transports** (the "route variants" case): the pool is sized to distinct logical
  routes, not to `len(transports)`.

- **When the bottleneck is per-flow (the common case here), non-transport-disjoint
  logical routes still aggregate.** This mux's per-route no-skip reorder frontier
  is itself a per-flow limiter — a single route stalls below the link's real
  capacity when one packet head-of-line-blocks it. Parallel logical routes over the
  *same* physical path are independent reorder domains, so they collectively use
  more of that link than one route can — the same principle as MPTCP subflows
  sharing a link, or a download accelerator's parallel connections beating a
  single-flow rate limit. Only when the physical link is genuinely saturated do
  co-transport routes stop aggregating (they then merely share it) — at which point
  transport-disjointness is what buys more capacity.

- **Consequence for the active-set rule.** The `--tunnels` guarantee "the union of
  all tunnels' active legs must not double-use a route" becomes "…must not
  double-use the **binding bottleneck**." Two tunnels may share a transport via
  distinct route-IDs and still aggregate while the per-flow reorder is the limit;
  they must diverge onto transport-disjoint paths only once that shared link
  saturates. So the shared pool **admits co-transport logical routes**, and the
  scheduler spreads the active set across bottlenecks — escalating to
  transport-disjoint legs only when a link is the actual constraint. It also means
  standby (warm) routes may freely overlap on transports; disjointness is an
  active-set-union property, not a per-leg or per-tunnel one.

Phase 3's shared route-ID trunk is the mechanism that makes co-transport logical
routes cheap to hold warm (one reserved chain, many demuxed consume rules), so
this governing model and Phase 3 are the same end-state seen from two angles.

## Migration / coexistence

Phase 1 coexists with the per-group pool with zero negotiation: it is a
read-through cache under the existing dial path. Groups that never hit the cache
behave exactly as today. There is no flag and no wire bit because nothing leaves
the visor. Phases 2–3 layer on top — the pin is still local, and only the trunk
(phase 3) introduces a capability bit, at which point mixed fleets fall back to
per-group chains automatically.

## Prototype

- `pkg/router/warm_route_pool.go` — the `warmRoutePool` cache (put / bestPlan /
  invalidate / stats), fully unit-tested in `warm_route_pool_test.go` (hit,
  miss, min-hops keying, transport/intermediate exclusion, TTL expiry, first-hop
  dedup, invalidate, nil-safety).
- `pkg/router/router.go` — one field + one initializer, mirroring `tpdCache`.
- `pkg/router/router_dial.go` — `addOneAuxLeg` consults then populates the pool
  around its `fetchBestRoutes` call; all downstream invariant gates unchanged.

Not prototyped (documented above): the phase-2 warm-transport pin and the
phase-3 capability-negotiated trunk.
