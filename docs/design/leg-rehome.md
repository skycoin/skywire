# Leg re-home: adopting a standby tunnel's chain as a mux leg

An ACTIVE route group `G` takes over the already-built route chain of a STANDBY
route group `S` and runs it as one of its own packet-level mux legs — in place,
with no setup-node dial. The proxy's standby pool then serves both purposes at
once: stream-level failover (a whole tunnel steps in when one dies) and
packet-level multiplexing (a pooled tunnel's chain becomes aggregation width).

## Why this is not the shared trunk

`docs/design/shared-warm-route-pool.md` ("Why the rules can't be shared")
establishes the constraint: the intermediate rules of a chain are pure
`routeID → (routeID, transport)` switches carrying no descriptor
(`pkg/routing/rule.go:443`), and the chain terminates at exactly one
`ConsumeRule` whose descriptor selects the group at each edge
(`pkg/router/router_packet.go:112`: `desc := rule.RouteDescriptor()` →
`r.rgsNs[desc]`). Sharing one chain between two groups needs a demux — Phase 3,
a large refactor.

Re-home does not share. It **moves**: the chain keeps its one consume rule at
each edge, and both edges **rewrite that rule's descriptor** from `S` to `G`.
The intermediates never learn anything happened — their rules are descriptorless
— so no hop is touched and no setup node is called. `S` is left with no legs and
is closed. It takes the bit Phase 3 reserved: `CapLegRehome = 1 << 8`.

## Capability

`CapLegRehome uint16 = 1 << 8` (`pkg/routing/packet.go`), advertised
unconditionally in the route-group handshake alongside `CapLegState` /
`CapUniDir` (`pkg/router/route_group.go`, `sendHandshake`), activated only on
`local & remote` intersection — the same pattern as every other mux capability.
It requires `CapMux`. A peer without the bit never sees a `LegRehomePacket`;
the caller gets `ErrRehomeUnsupported`.

## The packet

`LegRehomePacket` (`pkg/routing/packet.go`), route ID = the leg's next-hop
route ID, so it rides the chain exactly like an aux-leg handshake or a
`LegStatePacket` and is identified by the route it arrives on.

Payload (13 bytes): `nonce(u64 BE) | flags(u8) | srcPort(u16 BE) | dstPort(u16 BE)`

- `nonce` ties request → ack → commit of one re-home.
- `flags`: `0x01` ack, `0x02` commit, `0x04` refused.
- `srcPort`/`dstPort` are **the sender's own** ports for the target group `G`.
  The receiver mirrors them (`NewRouteDescriptor(S.SrcPK, S.DstPK, dstPort,
  srcPort)`) to find its side of `G`. The PK pair comes from `S`'s descriptor,
  which is how "same peer" is enforced: a re-home can only move a chain between
  two groups that already join the same two visors.

Three messages, because the two edges must not send `G` frames on the chain
until the *other* edge has rewritten its rule:

1. **request** (initiator → exit, on `S`'s chain).
2. **ack** (exit → initiator, on the chain, after the exit's rewrite). Carries
   `0x04` instead when the exit refuses (no such group, wrong peer, mux off).
3. **commit** (initiator → exit, on the chain, now addressed to `G`) — the exit
   promotes the adopted leg out of standby.

## Local sequence (initiator)

1. Resolve `G` and `S`; refuse if `S` is not idle-standby, if either lacks mux,
   or if `G.mux.legRehomeEnabled` is false (`ErrRehomeUnsupported`).
2. Refuse if `S`'s first-hop transport is already a leg of `G` — the mux
   invariant `appendRouteToGroup` enforces (no two legs share a transport).
3. Send the request on `S`'s leg and wait for the ack (`legRehomeAckTimeout`,
   5 s). A refusal or a timeout leaves **both groups untouched**.
4. On the ack: rewrite `S`'s consume rule (same key route ID, new descriptor)
   and its forward rule, save both — `routing.Table.SaveRule` replaces by key —
   detach the leg from `S` *without* deleting its rules, and append it to `G`
   with `appendRules(fwd', rvs', tp, "rehome from :<port>")`.
5. Emit `leg_promoted` on `G` (reason `rehome from :<port>`) and
   `tunnel_consumed` on `S`.
6. Send the commit on the leg, now through `G`'s rules.
7. `S` has no legs left, so it closes with no close packets to broadcast (its
   `tps` is empty) — the chain must survive; it belongs to `G` now.

## Exit sequence

On a request arriving on `S`'s chain (dispatched to `S` by its consume rule):

1. Mirror the carried ports onto `S`'s PK pair, look up `G` in `r.rgsNs`, and
   verify it exists, has mux, and has the same `DstPK` as `S` (same peer).
2. Rewrite the leg's consume rule to `G`'s descriptor and save it; rewrite the
   forward rule likewise.
3. Detach the leg from `S`, append to `G` **as standby** (`setLegStandby`), so
   the exit — which is the bulk sender on a download — cannot stripe onto the
   chain before the initiator has rewritten its own rule.
4. Ack on the leg; close `S` as its last leg leaves.
5. On the commit, promote the leg (`setLegStandby(idx,false)` + `markLegReady`)
   and emit `leg_promoted` with the same reason.

## In-flight frames

The leg carries `G`'s sequence space only from the ack (exit side) / the local
rewrite (initiator side). Anything `S` had in flight on the chain arrives after
the rewrite, lands on the rewritten consume rule, and is handed to `G`'s reorder
buffer where its sequence is outside `G`'s window — dropped, counted as a
reorder drop, not retransmitted. This is why re-home is only offered on a
**standby** tunnel: a standby tunnel's yamux session is idle but for keepalives,
so the dropped set is empty in practice. `S`'s own SACK/retx state dies with
`S`; `G`'s is untouched, since a new leg adds capacity but not sequence space.

## App side

`S`'s yamux session dies when `S` closes, which to the proxy looks exactly like
a tunnel death — and a death re-arms the pool fill and the redial backoff
(`pkg/skysocks/client.go`, `retireTunnel`). That would be a redial storm for a
tunnel that was not lost but **spent**. So the visor queues
`AppOpConsumedTunnel` for the owning app after a successful re-home; the client
drops the tunnel from the pool, records `tunnel_consumed` instead of
`tunnel_retired`, and does **not** reset the backoff or arm a fill. The pool's
normal ceiling logic refills on its own schedule.

## Fallback

A peer without `CapLegRehome` (or a re-home the exit refuses) must not strand
the caller: the standby tunnel's chain is unusable as a leg, so the pool has to
be spent the dial-based way — a pool-sourced leg dialed through the setup node,
reusing the pool's route plan and warm transport. That path is being built in
parallel and is **not** implemented here. The call site is
`poolSourcedLegFallback` in `pkg/router/leg_rehome.go`, which returns
`ErrRehomeUnsupported` and is where that dial belongs.

## Operator surface

    skywire cli proxy mux adopt --tunnel <active dst_port> --from <standby dst_port>

`cmd/skywire-cli/commands/proxy/mux_ops.go` → `RehomeTunnelLeg` (visor RPC) →
`router.MuxLegController.RehomeStandbyLeg`. Both ports are the `dst_port`
`proxy mux info --json` prints. Events land in the router's mux ring, so
`visor state --select diag` and `proxy mux info --json` show the adoption.
