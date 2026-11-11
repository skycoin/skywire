# Mux leg model

A route group sends and receives over one or more legs. A leg is one route to
the peer. In `pkg/router/route_group.go` it is a `groupLeg` value: `tp` is the
first hop transport, `fwd` the forward rule for packets sent on the leg, and
`rvs` the consume rule for packets that arrive on it.

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

`routeMux.setLegStandby`, `markLegReady` and `isLegStandby` take an index. Call
them only under `rg.mu`. An index on a mux event or a leg-change hook call
records where the leg was.

## Where legs are added

`appendLegLocked` is the only append to `legs`. Its callers are `appendRules`
(paired), `appendForwardLeg` (forward-only) and `adoptRehomedLeg` (a chain
taken from a standby group). The first two fire the leg-change hook for every
leg but the first. `appendReverseLeg` is the only append to `rvsOnly`.

## Where legs are removed

Each of these calls `routeMux.removeLegs` with the same indices.

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

## Per-leg state outside the struct

Keyed by leg index, in `routeMux`: `legs` (the counters), `ready` and `standby`
are kept in step by `growLegs`, `removeLegs` and `swapLegs`. `legGroups` is
replaced each data-progress tick through `setLegGroupsFor`.
`transportSelector.Rebuild` rebuilds the selector state by index.

Keyed by transport ID, on `RouteGroup`: `legForwardHops`, `legRemoteTp`,
`legPongSeen`, `legMissed`, `legE2ELatency`, `legOWD`, `legRTTWin`,
`legRecvSnap`, `sbdTrials`, `sbdAckedPrev`, `adaptiveParks` and
`peerParkedLegs`. `inflightPings` maps a probe to a transport ID.
