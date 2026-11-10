# Mux leg model

A route group sends and receives over one or more legs. A leg is one route to
the peer. In `pkg/router/route_group.go` it is a `groupLeg` value.

- `tp` is the first hop transport the leg writes to.
- `fwd` is the forward rule for packets sent on the leg.
- `rvs` is the consume rule for packets that arrive on the leg.

`RouteGroup.legs` is a `[]groupLeg` and `RouteGroup.rvsOnly` is a
`[]routing.Rule`. `RouteGroup.mu` guards both.

## The three shapes

- A paired leg has `tp`, `fwd` and `rvs`. This is the normal case.
- A forward-only leg has `tp` and `fwd`, and `rvs` is nil.
- A reverse-only rule has no transport and no forward rule. It is not a leg and
  lives in `rvsOnly`, so every element of `legs` is a leg.

`legByConsumeRuleLocked` maps a consume rule to a leg index. It returns -1 for
a reverse-only rule. `pruneLegByConsumeRule` is the one place that still gives
a reverse-only rule an index, which is `len(legs)` plus its place in `rvsOnly`.

## Order

Index 0 is the primary leg. Ping, pong and the default SACK path use it. New
legs go on the end. Removing a leg shifts the later legs down by one, so the
leg at index 1 replaces a removed primary. `reelectPrimary` swaps leg 0 with
another leg and calls `routeMux.swapLegs` with the same pair.

## Where legs are added

- `appendLegLocked` is the only append to `legs`. Its callers are
  `appendRules` (paired), `appendForwardLeg` (forward-only) and
  `adoptRehomedLeg` (a chain taken from a standby group).
- `appendReverseLeg` is the only append to `rvsOnly`.

## Where legs are removed

Each of these calls `routeMux.removeLegs` with the same indices.

- `pruneDeadTransports` drops every leg whose transport is nil or closed.
- `pruneDeadLegs` drops the legs named by transport ID. Its callers are
  `dropLegsByIndex`, `handleTransportClosed` and `pruneLivenessDeadLegs`.
- `pruneLegByConsumeRule` drops the leg the peer retired.
- `RemoveMuxRouteByTransport` drops the leg an operator named.
- `detachLegForRehome` takes a leg out and keeps its rules for another group.

## Per-leg state outside the struct

Keyed by leg index, in `routeMux`, kept in step by `growLegs`,
`removeLegs` and `swapLegs`: `legs` (the counters), `ready`, `standby` and
`legGroups`. `transportSelector.Rebuild` rebuilds the selector state by index.

Keyed by transport ID, on `RouteGroup`: `legForwardHops`, `legRemoteTp`,
`legPongSeen`, `legMissed`, `legE2ELatency`, `legOWD`, `legRTTWin`,
`legRecvSnap`, `sbdTrials`, `sbdAckedPrev`, `adaptiveParks` and
`peerParkedLegs`. `inflightPings` maps a probe to a transport ID.
