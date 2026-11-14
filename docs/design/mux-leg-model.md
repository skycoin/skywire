# Mux leg model

A route group sends and receives over one or more legs. A leg is one route to
the peer. In `pkg/router/route_group.go` it is a `groupLeg` value: `tp` is the
first hop transport, `fwd` the forward rule for packets sent on the leg, and
`rvs` the consume rule for packets that arrive on it. `hops` is the leg's full
forward route and `remoteTp` the far end's first hop transport for it. Both
are set by `recordLegRoute` once the leg is in place.

`RouteGroup.mu` guards `legs` (`[]groupLeg`) and `rvsOnly` (`[]routing.Rule`).

## The three shapes

- A paired leg has `tp`, `fwd` and `rvs`. This is the normal case.
- A forward-only leg has `tp` and `fwd`, and `rvs` is nil.
- A reverse-only rule lives in `rvsOnly`. It is not a leg and has no index.

## Order

Index 0 is the primary leg. Ping, pong and the default SACK path use it. New
legs go on the end, and removing a leg shifts the later legs down by one.
`reelectPrimary` swaps leg 0 with another leg, in step with `swapLegs`.

## Addressing a leg across a lock release

A leg index is only valid while `rg.mu` is held. A prune or a primary swap
changes what an index means, and many control paths release the lock to write
to the network. So a leg is named by its transport ID, which does not move.

- A decision taken under the lock leaves it as a transport ID.
- The action resolves the ID to the current index under `rg.mu` when it acts,
  and does nothing if the leg is gone. The helpers are in `leg_identity.go`:
  `legIndexLocked`, `legByID`, `legStandbyByID`, `setLegStandbyByID` and
  `sendLegStateByID`.
- An index from outside, such as the legs `on_tick` returns, is translated
  with `legIDsAt` against `legIDsLocked`, read in the same critical section as
  the snapshot the policy was shown. It is never used as an index again.
- A packet names its leg by the consume rule it arrived on. That is resolved
  and acted on in one critical section, or turned into an ID first
  (`legChainByConsumeRule`).

`routeMux.setLegStandby`, `markLegReady`, `isLegStandby` and `legAt` take an
index. Call them only under `rg.mu`. An index on a mux event or a leg-change
hook call records where the leg was.

## The mux's record of a leg

`routeMux.legs` holds one `muxLeg` per leg, in the same order as
`RouteGroup.legs`. The record is the mux's whole per-leg state.

- `id` is the leg's transport ID.
- `ready` says the peer is known to have the leg's rule, so it may be picked.
- `standby` keeps the leg installed but out of the send set.
- `group` is the shared-bottleneck group id, or `noLegGroup`.
- The embedded `legCounters` hold the traffic counters and the scheduler's
  per-leg estimates.

`legMu` guards the slice and `ready`, `standby` and `group`. The traffic
counters are atomic.

A removal or a swap moves the whole record, so the parts cannot fall out of
step with each other. The invariants are kept in one place each. `addLegLocked`
makes leg 0 ready and never standby, and makes an aux leg not ready, and
standby when `standbyNewLegs` is set. `assertPrimaryLocked` makes whatever leg
lands in slot 0 ready and not standby, after a removal and after a swap.

The records are kept in step with `RouteGroup.legs` by three calls, all under
`rg.mu`. `syncMuxLegsLocked` follows every append, `compactMuxLocked` every
removal and `swapLegs` the primary swap. `setLegStandby` still makes a record
for a leg the mux was not told of, and the next `syncLegs` names it.

Group ids are leg indices, so a removal or a swap clears every record's
`group`. The next data-progress tick sets them again through
`setLegGroupsFor`.

## Counting traffic

A pointer to a leg's `muxLeg` names the leg for as long as anyone holds it. An
index does not. So the counters are credited through the record, never by
index.

- The send path picks a leg under `rg.mu`. `nextTransport` and
  `nextRetxTransport` return the picked leg's record with the transport and
  the rule. `write`, `resendSeqs` and `resendOnAnotherLeg` call `recordSent`
  and `recordRetransmit` on it after the write, with the lock released.
- The receive path resolves the arrival leg from the consume rule under
  `rg.mu`, calls `recordRecv` there, and hands the record to `deliverData`,
  which calls `recordPayload` or `recordDup` after the lock is released.
- `recordRecv` and `recordRepair` on a repair frame run under `rg.mu`.

The record methods take no lock. A write that finishes after its leg was
removed is counted on the removed record, which nothing reads.

## Where legs are added

`appendLegLocked` is the only append to `legs`. Its callers are `appendRules`
(paired), `appendForwardLeg` (forward-only) and `adoptRehomedLeg` (a chain
taken from a standby group). The first two fire the leg-change hook for every
leg but the first. `appendReverseLeg` is the only append to `rvsOnly`. Each
caller of `appendLegLocked` then calls `syncMuxLegsLocked`.

## Where legs are removed

Each of these calls `compactMuxLocked`, and so `routeMux.removeLegs`, with the
same indices.

- `pruneDeadTransports` drops every leg whose transport is nil or closed.
- `removeLegsByID` drops the legs named by transport ID. `pruneDeadLegs` wraps
  it and then wakes rotation and self-heal, for `dropLegs`,
  `handleTransportClosed` and `pruneLivenessDeadLegs`. `closeReleasedLeg`
  calls `removeLegsByID` directly.
- `pruneLegByConsumeRule` drops the leg the peer retired. For a reverse-only
  rule it deletes the rule and touches no leg.
- `RemoveMuxRouteByTransport` drops the leg an operator named.
- `detachLegForRehome` takes a leg out and keeps its rules for another group.

`dropLegs` and `closeReleasedLeg` leave the transport open, because other
groups may ride it, and send `CloseLegRetired`. They close it only for a peer
without CapLegState.

## Per-leg state outside the two records

Keyed by leg index: `transportSelector.Rebuild` rebuilds the selector state by
index, and `fecStripeUsed` counts frames per leg index for one FEC block.

The maps below are keyed by transport ID. Each stays a map because its lock is
not the lock of either record. `groupLeg` is under `rg.mu` and `muxLeg` under
`legMu`. Moving a map would mean taking one of those where the map's own leaf
lock is taken today, or merging the locks.

On `RouteGroup`, under `legLivenessMu`, which is never held while taking
`rg.mu`:

- `legPongSeen`, `legMissed` and `inflightPings`. The pong handler writes them
  without `rg.mu`. `resetLegLiveness` clears the first two when a leg is
  added, and the liveness tick drops entries for legs that are gone.
- `legE2ELatency`, `legOWD` and `legRTTWin`. The pong handler writes them too.
  `RouteGroup.snapshotLegs` drops entries for legs that are gone.
- `legRecvSnap`. The data-progress tick owns it and drops entries for legs
  that are gone.

On `RouteGroup`, under `sbdTrialMu`, a leaf lock:

- `sbdTrials`. A trial for a leg that is gone is dropped when it is next
  arbitrated.
- `sbdAckedPrev`. Nothing prunes it. The value is a baseline for a cumulative
  count, so a stale entry is not misread.
- `sbdIndependent` and `sbdRuledAt` are keyed by a pair of transports and are
  about the paths, not the legs.

On `RouteGroup`, under `adaptiveParkMu`, a leaf lock: `adaptiveParks`. It is
cleared on a promote or an operator pin, not when the leg leaves.

On `RouteGroup`, under `peerParkMu`, a leaf lock: `peerParkedLegs`. Each
removal path calls `forgetPeerPark`.

On `routeMux`: `ackDelayByTp` and `sbdFoldNano` under `ackDelayByTpMu`, and
`legE2EByTp` under `legE2EMu`. The SACK and pong paths write them without
`legMu`. Nothing prunes them.

`legForwardHops` and `legRemoteTp` were `rg.mu` maps that nothing pruned. They
are now `groupLeg.hops` and `groupLeg.remoteTp`.
