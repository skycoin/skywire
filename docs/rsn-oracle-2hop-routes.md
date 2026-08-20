# RSN-oracle 2-hop route calculation (phase-1)

## Summary

An **OPTIONAL, opt-in** route-calculation path for **single-intermediate
(2-hop) multiplexed routes** `S → I → D` that gets the destination's transports
**directly from the destination**, authorized by the transport/route setup-node
(RSN) acting as a **signing oracle**, instead of relying on the
transport-discovery service (TPD).

### Why

For a 2-hop route `S → I → D` the only inputs needed are:

- **S's own transports** — known locally, always fresh.
- **D's own transports** — authoritative and fresh **from D itself**.

The usable intermediate set is exactly the overlap of the two peer sets:

```
intermediates(S,D) = { I : S has a live transport S–I } ∩ { I : D has a transport I–D }
```

So a 2-hop route can be computed **entirely locally** once D's transport list is
in hand — **TPD is only needed for routes with ≥ 2 intermediates**. This makes
the most common route type independent of TPD's global view (currently ~9%
complete, being fixed separately).

### Reused mechanism

The RSN is already a **pure signing oracle** in the source-driven cascade
route-setup protocol (`pkg/router/cascade_source.go:6`): it signs per-hop
capabilities for a specific target PK and is never on the data path. Targets
honor only allow-listed RSNs. This design reuses that exact primitive:

- `CascadeSetup.Sign(targetPK, rsnSK)` / `Verify(targetPK)`
  (`pkg/routing/cascade.go:92,103`) — the RSN signs FOR a target; the target
  verifies against its own PK.
- `ErrCascadeUntrustedRSN` + `CascadeHandler.trustedRSNs`
  (`pkg/router/cascade_handler.go:24,97`) — targets honor only allow-listed RSNs.
- `transport.CompactEntry` + `EntryFromCompact`
  (`pkg/transport/entry.go:75,96`) — the compact remote-edge+type wire form D
  returns, reconstructed by S into canonical entries.

## Protocol: the transport-list query

A new control-plane message, authorized exactly like a cascade layer.

```
                 (1) SignTransportQuery RPC                (over existing dmsg setup conn)
   S  ───────────────────────────────────────────────▶  RSN
      ◀───────────────────────────────────────────────
                 signed TransportQuery{RSN,Target=D,Requester=S,Nonce,Sig}

                 (2) deliver signed query  (PHASE 1: dmsg-direct to D)
   S  ───────────────────────────────────────────────▶  D
      ◀───────────────────────────────────────────────
                 TransportQueryResponse{Target=D, []CompactEntry}   (D verifies RSN sig + trust)

   (3) S computes  intermediates = (S's own peers) ∩ (D's peers)   — LOCAL, no TPD
       and builds the disjoint 2-hop mux legs.
```

1. **S → RSN (RPC):** `SetupRPCGateway.SignTransportQuery` mints a nonce and
   signs a `TransportQuery` binding `(RSNPK, TargetPK=D, RequesterPK=S, Nonce)`.
   The RSN reuses the keypair the cascade builder already holds
   (`g.Cascade.rsnPK/rsnSK`). It does **not** contact D.
2. **S → D (delivery):** S carries the signed query to D. **Phase 1** delivery is
   **dmsg-direct to D over the always-available dmsg relay** — the same way
   `tp --remote` reaches a remote visor over dmsg. **Phase 2 (TODO)** is carrying
   the query over transports via intermediates, exactly like the cascade's own
   `RelayTpID` + nested `Payload` hop-by-hop mechanism.
3. **D verifies + responds:** D checks the RSN signature against **its own PK**
   and its **trusted-RSN allow-list**, then returns its own live, non-setup
   transports as `[]transport.CompactEntry` (remote edge + type). Same exclusions
   as the local route calc: closed/black-holed and `LabelSetup` transports are
   never advertised.
4. **S computes locally:** reconstruct D's entries via `EntryFromCompact(D, ce)`,
   intersect with S's own transports, and build the disjoint 2-hop legs — one per
   distinct intermediate PK — feeding the existing mux establishment.

### Authorization (the whole point)

The RSN signs; **D checks its trusted-RSN allow-list** — identical to the
cascade trust model. The signature binds the query to a single `(target,
requester)` pair, so it cannot be replayed against another visor or claimed by
another requester. `cipher.Sign/Verify` + the trusted-RSN allow-list are reused
verbatim (`TransportQuery.Sign/Verify` mirror `CascadeSetup.Sign/Verify`).

## Implementation (this branch)

| Piece | Location |
|---|---|
| Query message + `Sign`/`Verify` + JSON marshal | `pkg/router/transport_query.go` (`TransportQuery`) |
| D-side verify + respond handler | `pkg/router/transport_query.go` (`BuildTransportQueryResponse`) |
| RSN signing endpoint (RPC) | `pkg/router/setup_rpc_gateway.go` (`SetupRPCGateway.SignTransportQuery`) |
| S-side RSN RPC client method | `pkg/router/setupclient.go` (`SetupClient.SignTransportQuery`) |
| **S→D dmsg-direct delivery (deliverer)** | `pkg/router/transport_query_dmsg.go` (`dmsgTransportQueryDeliverer`, `NewDmsgTransportQueryDeliverer`) |
| **D-side dmsg listener + RPC gateway** | `pkg/router/transport_query_dmsg.go` (`TransportQueryRPCGateway.Query`, `ServeTransportQueryListener`) |
| Dedicated dmsg port | `pkg/skyenv/skyenv.go` (`DmsgTransportQueryPort = 68`) |
| Composed production oracle | `pkg/router/transport_query_dmsg.go` (`NewDmsgRSNOracle`) |
| S-side fetch-D's-transports-via-oracle | `pkg/router/rsn_oracle_routes.go` (`fetchDstTransportsViaOracle`) |
| **Local 2-hop disjoint route computation (tested crux)** | `pkg/router/rsn_oracle_routes.go` (`computeDisjoint2HopRoutes`) |
| Router-facing oracle seam | `pkg/router/router.go` (`DstTransportOracle`, `Router.SetDstTransportOracle`), `pkg/router/rsn_oracle_routes.go` (`router.oracle2HopRoutes`) |
| Dial-path plug-in (gated, default OFF) | `pkg/router/router_dial.go` — top of `fetchBestRoutes` |
| Config flag (`RouteOption`) | `pkg/router/router.go` (`Config.EnableRSNOracleRoutes`, `DialOptions.UseRSNOracle2Hop`) |
| Config surface + plumbing | `pkg/visor/visorconfig/v1.go` (`Routing.EnableRSNOracleRoutes`), `pkg/visor/visorcore/router.go`, `pkg/visor/init_router.go` (oracle + listener wiring) |
| Unit tests | `pkg/router/transport_query_test.go`, `pkg/router/rsn_oracle_routes_test.go`, `pkg/router/transport_query_dmsg_test.go` |

### End-to-end wiring (dmsg-direct, phase-1)

`pkg/visor/init_router.go` (in `setupRouting`, after the route-checker setup):
when `Routing.EnableRSNOracleRoutes` is true it (a) calls
`r.SetDstTransportOracle(router.NewDmsgRSNOracle(v.dmsgC, EffectiveRouteSetupNodes(), log))`
so the **source** side is live, and (b) starts
`router.ServeTransportQueryListener(serveCtx, v.dmsgC, gw, log)` in a goroutine so
the **destination** side answers queries. Both are skipped entirely when the flag
is off — no extra dmsg listener, no dial-path change.

- **Delivery transport:** the deliverer dials the destination at
  `dmsg.Addr{PK: D, Port: skyenv.DmsgTransportQueryPort(=68)}` and issues the
  `TransportQueryRPCGateway.Query` RPC over the dmsg stream — the same
  net/rpc-server + gob-client pairing the setup-node path uses
  (`embedded_route_setup.go` serves `SetupRPCGateway` the same way), so no new
  codec or port-multiplexing is introduced.
- **Listener/port:** a **dedicated** dmsg port `68` (`DmsgTransportQueryPort`),
  registered in `pkg/skyenv/skyenv.go` and guarded by `ports_test.go`. It is a
  control-plane listener, bound **only** when the flag is on. It does **not**
  require any new capability — it reuses the visor's existing dmsg client and PK
  identity.

### Where it plugs into route setup (exact seams)

- **`fetchBestRoutes`** (`pkg/router/router_dial.go:811`) is the single funnel
  the whole dial path (primary route + every mux leg via `establishMuxRoutes` /
  `addOneAuxForwardLeg` / `GrowMuxRoute`) uses to obtain forward/reverse hops.
  The RSN-oracle fast path is inserted at the **top** of `fetchBestRoutes`, after
  the `forceLocal` check: gated on
  `(Config.EnableRSNOracleRoutes || opts.UseRSNOracle2Hop)`, a wired oracle, and
  a **2-hop-satisfiable** min-hops constraint (`≤ 2`; a `min_hops ≥ 3` request
  falls through). Because each `establishMuxRoutes` iteration adds the used
  intermediate to `opts.ExcludeIntermediatePKs`, successive calls return
  **disjoint** intermediates automatically — the same discipline the existing
  disjoint-mux code uses.
- **On any miss** (no oracle wired, no shared intermediate, delivery error) it
  falls through to the existing route-finder / TPD-backed path. **Zero behavior
  change when disabled.**

### The local computation (the tested standalone function)

`computeDisjoint2HopRoutes(src, dst, localTps, dstEntries, opts, max)` is a pure
function of its inputs (no I/O), so it is unit-tested with mock S/D transport
sets:

- intermediate `I` qualifies iff S has a live **non-DMSG** `S–I` **and** D has a
  **non-DMSG** `I–D`, `I ∉ {src, dst}`, `I ∉ opts.ExcludeIntermediatePKs`, and
  the `S–I` id `∉ opts.ExcludeTransportIDs`;
- DMSG is excluded on both legs (a dmsg relay hop is unaccountable in a multihop
  route — same rule as the local BFS in `calculateLocalRoutes`);
- when either side has several transports to the same `I`, the most-preferred
  type wins (`tptypes.TypePreference`);
- the `I–D` transport id is the **canonical deterministic** id
  (`MakeTransportID(I, D, type)`) both endpoints agree on — reconstructed from
  the compact entry, exactly the id the intermediate uses to relay to D;
- legs are sorted by intermediate-PK for determinism and capped at `max`
  (`0` = uncapped). One leg per distinct intermediate ⇒ the set is disjoint.

## What is deferred (phase-2 / optional follow-ups)

Phase-1 is now **end-to-end functional** behind the default-OFF flag: protocol,
RSN signing endpoint, D-side response builder + dmsg listener, S→D dmsg-direct
delivery, the composed oracle, the local 2-hop computation, and the dial-path
plug-in are all implemented, wired, and tested. Remaining items are optional:

1. **Phase-2 delivery over transports via intermediates** — reuse the cascade
   `RelayTpID` + nested `Payload` carry so the query needs no dmsg at all.
2. **Config → `DialOptions.UseRSNOracle2Hop`** per-dial mapping if a per-app /
   per-policy opt-in (rather than the visor-wide `Config.EnableRSNOracleRoutes`)
   is wanted; the field already exists and is honored by `fetchBestRoutes`.
3. **Response caching** per `(src,dst)` with a short TTL so a mux build doesn't
   re-fetch D's list once per leg (functionally correct today; an optimization).
4. **`SetupClient` reuse** — `dmsgRSNOracle.DstTransports` opens a fresh
   setup-node connection per call (matching the cascade path's per-request model
   in `wrappers.go`); a pooled/relay path could be added later.

## Safety / invariants

- **Default OFF**, and inert even when on until an oracle is explicitly wired —
  no existing dial path changes.
- Reuses the cascade trust model verbatim: **RSN signs, D checks its trusted-RSN
  allow-list** — no new trust surface.
- Never uses DMSG or `LabelSetup` transports as data hops.
- 2-hop only; anything needing ≥ 2 intermediates still goes through TPD.
