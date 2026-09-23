# The mux shape axis: moving a session between stream level and packet level

Status: design, 2026-09-23, against develop `1b0d46a4d`.

The operator's requirement: *"the shape of the routes being multiplexed needs
to be able to change dynamically. routes need to be able to change from stream
level to packet level or vice versa, trivially ... and it should be possible to
work with a variety of different multiplexing configurations along this axis
which can perform similarly but may look very different from each other."*

Packet-level legs exist for **privacy and instant fail-over**, not throughput.
A shape that spends chains on legs will move less data than the same chains
spent on tunnels; that cost is accepted and is not a reason to refuse a move.

Companion docs: `docs/design/standby-tunnel-pool.md` (the pool),
`docs/design/leg-rehome.md` (the chain move), and
`docs/design/route-multiplexing-test-plan.md` (the live campaign).

## 1. The shape model

A session is one (app, exit) pair. It owns a multiset of **tunnels** — route
groups the dialling app marked `tunnelRoleActive`, each carrying a
`yamux.Session` — plus a **standby pool** of route groups holding one chain
each (`pkg/router/pool_arbiter.go:tunnelRoleActive`, `standbyTunnel`).

Each tunnel `T_i` has `n_i ≥ 1` legs. The **shape** is `(k; n_1…n_k)`, written
`k x n` when uniform. `4x1` is pure stream level: four tunnels, no packet
striping. `1x4` is pure packet level: one tunnel, four legs. `2x2` is today's
default. The **chain budget** is

    C = Σ n_i + |pool|

and every shape move conserves `C`. Only dialling and closing change it.

### The four primitive moves

| move | meaning | today |
|---|---|---|
| standby → leg | compose | **exists**: `pool_arbiter.go:poolArbiterStepExcluding` → `leg_rehome.go:rehomeChain`, falling back to `GrowMuxFromPool` when the peer drops the re-home packet; provenance in `rg.poolTaken` via `notePoolLegTaken` |
| leg → standby | decompose | **in flight** (`feat/leg-rehome-reverse`). On develop `releasePoolLegs` → `releaseLegByTransport` **closes the transport**: `C` drops by one |
| standby → tunnel | promote | **exists**, app side: `pkg/skysocks/client_live_ops.go:reconcileActiveSet` → `client.go:promoteBestStandby`, and the discretionary `tunnel_promoter.go:maybePromote` → `promoteTunnel`; event `tunnel_promoted` |
| tunnel → standby | demote (park) | **exists but destroys chains**: `client.go:parkTunnel` marks the session standby, and the router's `pool_arbiter.go:shedLegsForStandby` then closes *every leg above the first* |

Two of the four conserve `C`. Both failures are the same failure: a leg that
stops being a leg is closed rather than handed back. Land the reverse re-home
and demotion becomes "decompose to one leg, then park" — no new mechanism.

## 2. One knob: `mux.shape`

Register `mux.shape` in `pkg/router/routersettings/catalog.go` beside
`PoolActiveWidth` (line 167). Grammar:

    auto | <k>x<n> | <n1>,<n2>,…        e.g. auto, 2x2, 4x1, 1x4, 2,1,1

Default `auto`. Live-settable through `skywire cli route settings`, per-app via
`--app`, exactly like `pool.active_width`. The catalog has no string kind today
(`RegisterMin` / `RegisterZeroable` / `RegisterBool` / `RegisterList`), so a
`RegisterEnum` is the first thing the implementation adds.

The value is a **target**, never an assertion. Convergence obeys:

- **I1** never leave fewer than `pool.min_standby` in the pool — the check
  already in `poolArbiterStepExcluding` (`len(cands)-1 < PoolMinStandby`).
- **I2** no two legs of one tunnel on one first hop (`pool_legs.go:poolPlanAdmitted`,
  `TestWarmRoutePool_DedupByFirstHop`).
- **I3** no two *active tunnels* on one first hop (#5137,
  `TestPoolArbiterNeverPutsTwoActiveTunnelsOnOneFirstHop`).
- **I4** converge **only** by the four moves. Dial only when the pool is below
  `pool.min_standby`; close only when it is above its cap.
- **I5** one move per arbiter tick per session (`poolArbiterRound`), and a
  compose still waits out `pool.leg_interval` (`poolTakeDue`).
- **I6** `k ≥ 1`, `n_i ≥ 1`. A tunnel never reaches zero legs — which is why
  demotion must decompose first, not park first.
- **I7** never demote a tunnel that is carrying a stream; reuse
  `worstIdleActive` and the rule proven by
  `TestPromoter_NeverSwapsWhileTheActiveTunnelCarriesAStream`.
- **I8** `pool.freeze` and `tunnel.freeze_active` (`pkg/skysocks/settings.go`)
  freeze shape convergence too. An operator hold outranks the target.

## 3. The `auto` policy

`auto` is today's behaviour, restated as a shape:

| signal | source | target |
|---|---|---|
| no load, `pool.compose_idle=true` | `poolIdleWidth` (`pool_arbiter.go:214`) | `k = tunnel.count`, `n = pool.active_width` |
| load sustained above `pool.load_min_bps`, or `forwardFanoutActive` latched | `poolLoadSignal` (`:264`) | widen `n` to `muxWidthTarget` (`:183`) |
| idle past `pool.leg_release` | `releasePoolLegs` (`:540`) | narrow `n` back to `poolIdleWidth` |

With `pool.active_width=2` and `pool.compose_idle=true`, `auto` resolves to
today's `tunnel.count x 2`. **The default shape is bit-for-bit what develop
does now.**

What happens to the existing knobs:

| knob | under `mux.shape=auto` | under an explicit shape |
|---|---|---|
| `pool.active_width` | unchanged, it *is* the auto width | derived from `n`; ignored |
| `mux.app_width` | unchanged per-app override | derived; explicit shape wins |
| `tunnel.count` (skysettings) | unchanged | derived from `k` |
| `pool.compose_idle` | unchanged: holds `n` while idle | ignored — an explicit shape is always held |
| `pool.min_standby`, `pool.leg_interval`, `pool.leg_release`, `pool.allow_duplicate_route` | unchanged | unchanged — these govern *how* it converges, not *what to* |

Nothing is removed. An operator who never touches `mux.shape` sees no change.

## 4. Observability

`MuxInfo` (`pkg/router/route_group.go:689`) gains, next to `TunnelRole`:

- `Shape string` — the session's current shape as measured, e.g. `"2x2"`;
- `ShapeTarget string` and `ShapeSource string` (`"auto"` / `"operator"`);
- `LastMove struct{ Move, From, To, Reason string; At time.Time }`;
- `MoveCounts map[string]uint64`, keyed by the four move names.

The move names are the mux events that already exist
(`pkg/router/mux_events.go:128,129,143,144`): `pool_leg_taken`,
`pool_leg_released`, `tunnel_promoted`, `tunnel_parked`. The counters make a
shape history readable without walking the event ring.

`visor state --select mux` prints the session line first — current shape,
target, source, last move and its reason — then the per-tunnel legs it prints
today. `proxy mux info --json` carries the same fields.

Control flow and the log lines to expect:

    $ skywire cli route settings set mux.shape=4x1
    mux shape: target 2x2 -> 4x1 (route settings)
    pool_leg_released: leg on tp <id> returned to the pool as standby :30045 (shape 2x2 -> 4x1)
    tunnel_promoted:   standby :30045 -> active (shape 2x2 -> 4x1)
    mux shape: 3x1, 1 move to go
    mux shape: reached 4x1 in 3 moves

A blocked move says so rather than retrying silently:

    mux shape: 4x1 deferred; taking it would leave 1 standby and pool.min_standby=2
    mux shape: 1x4 deferred; the only standby shares first hop <pk> with leg 0 (I2)

## 5. Implementation, in order

Every PR is gated by `go test ./pkg/router -run 'TestStreamBench|TestEmu'`
(`pkg/router/emu_stream_test.go:594`) with no regression against the recorded
baseline, plus a rig smoke on the fleet before merge.

1. **Shape vocabulary, read-only.** New `pkg/router/mux_shape.go`:
   `parseShape`, `Shape.String`, `sessionShape(pool []*RouteGroup) Shape`.
   Populate `MuxInfo.Shape`. No behaviour change.
   Tests: `TestParseShapeRoundTrip`, `TestParseShapeRejectsZeroLegs`,
   `TestSessionShapeCountsTunnelsAndLegs`.
2. **The knob.** `RegisterEnum` in `routersettings`, then `MuxShape` in
   `catalog.go`; wire `route settings` and `--app`. Still advisory.
   Tests: `TestShapeKnobRejectsGarbage`, existing
   `TestCatalogNamesAreUniqueAndDotted` stays green.
3. **Conserve on release.** On top of `feat/leg-rehome-reverse`: make
   `releasePoolLegs` (`pool_arbiter.go:540`) hand the chain back as a standby
   group, keeping `releaseLegByTransport` only as the fallback when the peer
   never negotiated `CapLegRehome`.
   Tests: `TestReleaseReturnsAChainToThePoolNotTheCloser`,
   `TestChainCountConservedAcrossTakeAndRelease`.
4. **Conserve on demotion.** `shedLegsForStandby` (`:720`) decomposes each
   surplus leg through the same reverse re-home instead of closing it.
   Tests: `TestShedLegsForStandbyReturnsChainsToThePool`,
   `TestParkOfAWideTunnelConservesChains`.
5. **The converger.** `mux_shape.go:shapeStep(session, target, now)` called
   from `poolArbiterRound` (`:675`); picks at most one move per tick, checks
   I1–I8, and returns a reason when it declines. `auto` computes its target
   from §3 and reuses the current code path.
   Tests: `TestShapeStepMakesOneMovePerTick`,
   `TestShapeStepNeverBreaksMinStandby`,
   `TestShapeStepRefusesDuplicateFirstHop`, and in
   `scenarios_pool_arbiter_emu_test.go`:
   `TestEmuShapeConverges2x2To4x1`, `TestEmuShapeConverges2x2To1x4`,
   `TestEmuShapeRoundTripsBackTo2x2`.
6. **Promotion under the shape.** `reconcileActiveSet`
   (`client_live_ops.go:38`) takes `k` from the shape instead of `c.target`;
   the shape travels app↔router on the existing tunnel-role RPC
   (`pkg/app/appserver/rpc_ingress_gateway.go:validTunnelRole`).
   Tests: `TestReconcileActiveSetFollowsShape` beside the existing
   `TestReconcileActiveSetFollowsTunnelCount`.
7. **Surfaces.** `LastMove`, `MoveCounts`, the `visor state --select mux`
   session line, the log lines of §4.
   Tests: `TestMuxInfoReportsShapeAndLastMove`,
   `TestStateFieldSet_ShapeProjection`.

## 6. Where the code makes a move non-trivial

- **Provenance is keyed by transport.** `poolTakenLeg{tpID, from, how, at}`
  (`pool_arbiter.go:76`) and `notePoolLegTaken` (`:492`) prune by scanning
  `rg.tps` for a live transport. A chain that has been composed, released and
  composed again loses its origin port. The converger wants provenance keyed
  by **chain**, carried across both directions of the re-home.
- **Rule re-keying at the exit.** `rehomeChain` rewrites the one
  `ConsumeRule` descriptor at each edge; the intermediates are descriptorless
  and learn nothing (`docs/design/leg-rehome.md`). Decomposing a 2-leg tunnel
  therefore has to *create* a group at both edges for the departing chain —
  which is exactly what the reverse re-home builds, and why step 3 precedes
  everything else.
- **yamux session ownership.** A tunnel is a `yamux.Session` over a route
  group (`c.standby`, `c.sessionsMu`). A chain returned by a reverse re-home
  has no session. Before `promoteBestStandby` can see it, a client session
  must be opened on the fresh group — cheap, but it has to happen in the
  standby→tunnel move and not be discovered by the promoter.
- **`muxWidthTarget` takes a maximum.** `pool_arbiter.go:183` returns
  `max(selfHealTarget, app width)`, so an explicit shape *narrower* than the
  group's dial-time self-heal target can never be reached. Step 5 must derive
  `selfHealTarget` from the shape as well, or `1x4 -> 4x1` stalls at the
  self-heal floor.
- **Port allocation.** A standby tunnel is identified by its local port
  (`c.plan.port`, the `from` field, the `:30045` in every message above).
  A chain that returns to the pool takes a *new* port, so operator-facing
  output must not promise that a released leg comes back under the name it
  left with.
