# The spread policy

## What this is for, in the operator's terms

A bittorrent-like connection: stable enough for any traffic shaping, from one
route to many, along a performance↔privacy (anti traffic-analysis) spectrum.
Spread the traffic across many routes so no single link is saturated, and still
beat a single connection when the network allows it.

Both ends of that spectrum are settings, not builds. At the performance end each
route carries what it has been measured able to carry; at the privacy end every
route carries the same share whatever it can do, so the byte counts an observer
collects say nothing about which route is fast and no single observed route sees
most of an object. Everything between is a cap: no route above N % of an object.

## What we borrow from bittorrent, and what we do not

- **Requests in flight per peer ∝ its measured rate.** A bittorrent client
  pipelines more requests to a peer that answers faster. Here: capacity-
  proportional CHUNK ASSIGNMENT — an 8 MB/s route takes 8/11 of the object
  beside a 3 MB/s one, not half of it. This alone is the fix for the measured
  "a 3 MB/s tunnel drags the pair below the fast tunnel alone" behaviour.
- **Endgame.** Near the end of a torrent the remaining blocks are asked of
  several peers at once and the first answer wins, because one slow peer holding
  the last block decides when the whole thing finishes. Same tail, same fix.
- **Per-peer caps and connection bounds** — here `max_share` and `min_routes`.

What bittorrent never does is choose a peer's share for PRIVACY: it has no
threat model in which the distribution of bytes across peers is itself the leak.
`weight = even` is ours, not borrowed.

## The policy model

Four live knobs (`skywire cli proxy settings`, `pkg/skysocks/skysettings`), all
defaulting to today's behaviour:

| knob | kind | default | meaning |
|---|---|---|---|
| `spread.max_share` | ratio | `0.4` (was `1.0` = off until 2026-09-18) | largest fraction of one object's bytes any single route may carry |
| `spread.min_routes` | count | `3` (was `0` = off until 2026-09-18) | routes an object must be spread over |
| `spread.endgame` | bool | `false` | duplicate the tail chunks on the fastest idle route |
| `spread.weight` | enum | `rate` | `rate` = proportional to measured capacity, `even` = equal shares |

With none of them set the planner keeps its ledger and steers nothing: the
chunk lands wherever `pickSessionFor(pickRecv)` puts it today.

**The rule.** Per object, per chunk, from the object's own byte ledger:

1. Each route gets a weight — its metered capacity (`rate`) or 1 (`even`). A
   route with no measurement but a capacity PRIOR is weighed by that prior: the
   minimum `ThroughputBps` over its hops' transports, taken over its widest
   live leg, read from the visor over the `ProxyStatus` RPC and refreshed every
   `tunnel.prior_refresh`. A route with no measurement and no prior is a PROBE:
   it weighs the smallest weight present and may take exactly ONE chunk of the
   object, after which it sits out until a measurement re-weighs it. It is
   never credited the best weight present — that was the old rule, and it is
   how a standby promoted before the first chunk was handed the fattest share
   of an object on the strength of having carried nothing.
2. A route at or above `max_share` of the bytes placed so far is skipped, but
   only while another route is under its cap. A cap never stalls an object —
   and neither may the probe bound: with nothing else eligible, everything is.
3. Of what is left, the route furthest below its weight — smallest
   carried/weight — takes the chunk. Ties go to the route with the most
   measured capacity, then to the lowest index: the first chunk of an object is
   one big tie, and the session order is not a placement policy.

**Where it applies.** Range-split download chunk assignment across ACTIVE
tunnels (`rangesplit.go`) and striped-upload slot assignment (`upload_stripe.go`),
planned on different capacity estimates — `rxCapBps` and `txCapBps` — because a
busy window only proves the direction it moved bytes in. The LEGS inside one
route group are out of scope: that is the router's ECF scheduler, a different
layer on a different statistic. This policy chooses between GROUPS.

**The pool.** `min_routes` is reached by PROMOTING standby tunnels, before the
object's first chunk goes out. It never dials: the discovered pool is the
ceiling, and a policy asking for more routes than exist runs on the ones that
do. The route promoted is ranked in three tiers (`promoteFastestStandby`), NOT
on RTT: a standby with a MEASURED capacity in the object's direction first
(highest first), then one known only by its capacity PRIOR (highest first),
then — last, and only when there is nothing else — one with neither. The route
is being added to carry bytes, and capacity is the statistic the planner will
weigh its share by. With every candidate in the bottom tier this falls back to
the failover rank (`promoteBestStandby`, lowest RTT), which the failover paths
themselves still use unchanged. Standbys get their capacity numbers from the
idle audition, which offers up to `tunnel.audition_parallel` (3) of them a
sibling chunk stream at once, oldest-measured first, while nothing is busy. On the download path the promotion happens after chunk0's stream
is already open — chunk0 doubles as the size probe, and it is the reply to it
that first says the object is splittable at all — so chunk0 is charged to
whichever tunnel the browser's CONNECT landed on and only the remainder is
planned over the widened set.

**Width.** The download's in-flight budget follows the width once the policy
steers. `chunk.concurrency` is ONE object-wide admission gate, so three routes
under it simply share the same eight streams — about 2.7 each — and the object
finishes near a single route's solo rate however well the shares are balanced.
That is the whole of the first live run's download gap: 5.13 MB/s against an
8.23 MB/s best single-route reference (0.62×), while the uploads of the same
run, gated per tunnel (`inflight < live × upload.concurrency`), reached 0.91×.
Steering, the gate is `chunk.tunnel_concurrency` × the active width, measured
after `min_routes` has promoted; unset, and at a width of one, it is
`chunk.concurrency` exactly as before.

| knob | kind | default | meaning |
|---|---|---|---|
| `chunk.tunnel_concurrency` | count | `4` | chunks one route may carry at once on a split download the policy steers |

**Accounting.** The planner books a chunk's bytes against its tunnel at
admission and corrects the booking to what the tunnel actually carried when the
attempt ends, so an attempt that failed and went elsewhere leaves its first
tunnel charged for nothing. The choice and the booking are ONE critical section
— the pick that reads the ledger is the pick that charges it, and an open that
fails un-charges the reservation — because the admission gate above releases a
whole burst of chunks at once, and picks resolving against a ledger charged
afterwards all see the same all-zero shares and break the same tie the same way
(2026-09-16, `max_share` 0.4 / `min_routes` 3 over three active tunnels: all 12
chunks of a 50 MB download landed on the direct tunnel, 48.0 MB of 50).
On completion the object logs
`shares=<port>:<pct>,... top=<pct>`, keyed by the tunnel's local route-group
port — the same name `bench/direction.sh` reads out of `carrier.tsv`, so the
client's account can be matched against the per-transport `sent_delta` /
`recv_delta` measured at both ends.

## Criterion 10, and what the bench must show

> Under a spread policy that caps any one route at 40 % of the bytes and holds
> at least three routes, throughput ≥ 0.8 × the best single reference and no
> route exceeds its cap, from both ends' per-leg counters at 50 MB down and up;
> the policy is a live setting, not a build.

The bench set, on the frozen rig of `route-multiplexing-test-plan.md` §2:

| cell | knobs | what it must show |
|---|---|---|
| ref | none | the paired single-route reference, interleaved |
| spread-cap | `spread.max_share=0.4 spread.min_routes=3` | 50 MB down and up: top leg share ≤ 0.40 + one chunk, fan-out ≥ 3, goodput ≥ 0.8 × ref |
| spread-even | `+ spread.weight=even` | shares within ±5 % of each other; goodput reported, not gated |
| spread-endgame | `+ spread.endgame=true` | last-chunk latency down against spread-cap; wire/goodput ≤ 1.2 (criterion 4) |
| off | reset | byte-identical to the ref cell — the default is unchanged |

Shares come from both ends: the client's `shares=` line and `direction.sh`'s
per-transport deltas must agree within the standby-leg noise the script notes.

## Risks

- **Head-of-line on a capped fast route.** Chunks reach the browser IN ORDER, so
  capping the fast route parks a chunk on a slow one while the fast one idles: a
  0.4 cap over three uneven routes can cost more than the 0.8 × ref floor
  allows. That is what the bench cell measures; if it fails the answer is a
  higher cap, not a different rule.
- **Tail latency.** The last chunks have no successor to fill a fast route with,
  so one slow route sets the finish time. `spread.endgame` is the mitigation:
  once fewer chunks remain than there are routes, each is ALSO asked of the
  fastest idle route, the first answer wins and the loser's stream is closed
  under it. At most one duplicate per chunk and only onto a route that would
  otherwise sit out the object, so the extra bytes are bounded by the tail. They
  are wire, not goodput, and count against criterion 4.
- **`even` costs throughput by construction** — reported, not gated.

## Not done here

The endgame is on the plaintext download path only: a duplicate over HTTPS costs
a second TLS handshake to the origin and a duplicate upload chunk is charged
twice against the sink's reorder window, both unmeasured trades. And nothing
here chooses a point on the spectrum — what the default should be is a question
for the table, per criterion 5.
