# RFC: warm-standby mux legs + gated directions

Status: proposed. Author: routing team. Scope: `pkg/router` route-group leg
lifecycle + the policy `on_tick`/`on_leg_change` ABI.

## Problem

A multiplexed route group today has exactly two states per leg: **in the
group** (carrying its share of traffic) or **torn down**. Rotation and adaptive
policies (rotating-bw, latency-adaptive, elastic-mux, probe-and-prune) that
want to change the leg set therefore pay for it twice:

1. **Add latency.** Bringing a leg back means a full route setup — dial the
   transports, run the setup-node to install rules at every hop, handshake.
   Measured at ~8–9 s per leg for a min-hops-2 path on the live mesh.
2. **Capacity dips.** `dropLegsByIndex` (`route_group.go`) closes the dropped
   leg's transport and `DelRules`, so between a drop and the next add the group
   carries fewer legs. A live measurement of latency-adaptive under a fleet
   update showed the pathological case: when several intermediates restart at
   once, *every* min-hops-2 leg can die together and the group goes fully dark,
   because rebuilding 4 disjoint multi-hop routes through a churning fleet is
   slow. Warm spares would have let it promote instantly instead of rebuilding.

Rotation for bandwidth-spread, latency-adaptive's evict-slowest, and
elastic-mux's shrink all want to *move* legs, not destroy and recreate them.

## Core idea: keep the rules, gate the traffic

The setup-node's only irreducible job is installing routing rules at hops we do
not control. Once installed, a leg's forward and reverse rule-chains operate
independently and can be driven — or not — purely locally. So instead of
tearing a leg down, keep its rules installed and *gate* whether traffic flows.

Model each leg as a pair of gates `(forward, reverse)` over its warm-retained
rules:

| forward | reverse | leg state |
|---------|---------|-----------|
| 1 | 1 | bidirectional (today's active leg) |
| 1 | 0 | forward-only |
| 0 | 1 | reverse-only |
| 0 | 0 | **standby** — warm, carrying nothing |

**Standby is just `(0,0)`.** This collapses three previously-separate features
into one primitive: "keep a warm spare leg", "switch a route bidirectional↔
unidirectional", and "flip which direction a unidirectional leg flows" are the
same 2-bit-per-leg state machine over rules that are kept installed. Any
transition between the four states is a local gate toggle plus — when it changes
what the *peer* should send — one in-band control frame on the existing mux
control channel. The setup-node is re-involved only to install a genuinely new
leg's rule-pair or new hops, never to change how an existing leg is used.

## Why the existing machinery already supports this

The warm-standby leg needs three things to survive while carrying no data.
Verified against current `pkg/router`:

1. **Kept alive.** `sendKeepAlive` iterates **every** leg in `rg.tps` and sends
   a keepalive packet per transport regardless of whether it carries data — so a
   zero-traffic standby leg's rules stay refreshed at every hop.
2. **Not pruned.** `pruneDeadTransports` marks a leg dead only on
   `tp == nil || tp.IsClosed()` — transport-liveness, *not* traffic. An idle
   standby leg with a live transport is never pruned.
3. **Passes liveness.** `legLivenessServiceFn` prunes a leg only when it
   black-holes `legPongMissThreshold` consecutive *active* ping/echo probes. It
   probes every leg in `tps` and the peer echoes verbatim, so a standby leg
   still passes while carrying zero application data.

And the receive side is safe to quiesce: #3955's time-based reorder release
means a demoted leg that stops contributing sequence numbers cannot wedge the
reorder window (release is time-bounded, not gap-bounded). Before that fix this
primitive would have stalled the stream; it is unblocked *because* of it.

The send-side selector is already weighted (`selectTransport`, latency-weighted
with round-robin fallback; `DistributionWeighted`/`Weights[]`). A standby leg is
a leg with effective weight 0 — kept in `tps`, kept alive, but skipped by
`selectTransport`.

## Two warmth tiers

- **Transport-held, rules torn** — cheaper than cold (skips the transport dial)
  but still pays rule-setup on promote. This is what `prefer-connected` leans on
  via shared persistent transports.
- **Full hot-standby** — rules installed + kept alive + weight 0. Promote is
  instant. This RFC's target.

Warm-keeping the idle rules costs one routing-table entry per hop plus keepalive
traffic it is not forwarding on — cheap, and bounded by the standby-pool size.
So it is a **mode**: `asymmetric-lean` (delete the idle side, today's behaviour,
saves table entries) vs `asymmetric-warm` (keep + gate, enables free switching).
Dynamic "swap which direction is multiplexed" requires warm mode.

## Implementation surface

1. **Per-leg gate state.** Add a `(forwardActive, reverseActive)` pair (or a
   small weight/state enum) to the route group's per-leg arrays. `standby` =
   both false, weight 0.
2. **`selectTransport` / `rebuildWeights`** honour it: a standby (weight-0) leg
   is kept but never selected for sends; a demoted-reverse leg still receives.
3. **Rotation drop → demote.** The rotation/`on_tick` drop path chooses
   *demote-to-standby* (set weight 0, keep the `tps` entry + rules + keepalive)
   instead of `dropLegsByIndex`'s close+`DelRules`. Genuine eviction stays
   available for dead legs; demote becomes the default for policy-driven moves.
4. **Standby pool.** Maintain up to K warm spares over disjoint intermediates,
   refreshed by the existing keepalive/liveness loops; `on_tick` promotes/demotes
   between the active and standby sets.
5. **In-band direction control.** A new control opcode on the route group (the
   channel already carrying `MakeHandshakePacket(CapMux|CapSACK)`) tells the peer
   to quiesce/resume a direction — never the setup-node. Demote is otherwise a
   send-side-only decision (the leg still receives), so the receive path needs no
   coordination.
6. **`appendRouteAsymmetric` warm mode.** Today it *deletes* the unused
   direction's rule (making an asymmetric leg unidirectional-by-deletion, so
   flipping it back would need the setup-node). In warm mode it keeps + gates the
   idle side, so direction flips stay setup-node-free.

## Policy ABI additions

`on_tick`'s `RotationAction` today is `{DropLegs, AddLeg, ExcludeHops}` — it can
drop and add, not re-weight or gate. Add:

- `PromoteStandby []int` / `DemoteToStandby []int` — move legs between active and
  warm-standby without teardown.
- `SetDirection` (per-leg forward/reverse active flags) — the direction gate.
- a target **standby-pool size** — how many warm spares to keep.

These pair with the per-leg `{bps, rtt}` stats already delivered to `on_tick`
(#3968/#3969), so a policy can decide *which* leg to demote/promote on measured
performance. congestion-backoff (bleed weight off a congested leg toward
standby) becomes expressible; latency-adaptive/rotating-bw stop paying setup
latency on every adjustment.

## Proof / acceptance (the gate-5 test)

A rotation that **hot-swaps a warm standby leg with no throughput dip**, vs
today's tear-and-rebuild:

1. Establish a mux group + one warm standby leg over a disjoint intermediate.
2. Demote an active leg to standby and promote the spare in the same tick.
3. Per-leg telemetry (`proxy mux info` / the mux-bw harness) shows the leg set
   change with **no zero-throughput gap** — vs the current drop→(8–9 s
   setup)→add sequence that dips capacity.
4. Under a simulated intermediate restart, promotion from the standby pool keeps
   the group carrying traffic instead of going dark (the fleet-collapse case).

## Scope / sequencing

- **MVP:** per-leg standby state + demote-instead-of-teardown + `selectTransport`
  skip + `PromoteStandby`/`DemoteToStandby` ABI. This is enough for the gate-5
  proof.
- **Follow-on:** the direction gates (`SetDirection`) + in-band direction-control
  frame + `asymmetric-warm` mode. Unblocks the "one direction direct, the other
  multiplexed, swappable on demand" default routing policy.

This is core data-plane code (route-group leg lifecycle), so it lands
incrementally with unit tests per step and live validation on a settled fleet,
not as one large change.
