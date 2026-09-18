# Batched route setup

Serialized route setup does not make sense for multiplexed routes. A standby-pool
fill, `--tunnels N` and the mux leg self-heal all mean "give me N routes to the
same exit", and each of the N used to be its own route-finder/oracle query and
its own setup-node request.

## What the data already says

`routing.enable_rsn_oracle_routes` is on by default, so the source already asks
the destination for its live transports and intersects them with its own
(`pkg/router/rsn_oracle_routes.go`). Measured on the campaign rig 2026-09-18: the
local visor `0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c`
holds transports to 349 peers, the exit
`022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1` to 576, and
274 peers are common to both. `computeDisjoint2HopRoutes` returns all 274, ranked
— and `oracle2HopRoutes` then returned the first one and discarded the rest.

`pkg/router/oracle_plan_cache.go` keeps the set instead:

- **Singleflight + TTL** (`warm.plan_ttl`). N concurrent dials to one exit make
  ONE signed transport query; the rest wait on it and read the same set.
- **Claims** (`setup.plan_claim_ttl`). A dial's usual disjointness evidence is the
  sibling route groups it can see, which is exactly what a burst of concurrent
  dials does not have yet. The first caller claims the best candidate, the second
  the next, so N dials take N distinct intermediates. A claim is advisory: when
  every candidate is claimed the caller falls back to the best, which is the
  pre-claim behavior.

Every candidate is also stored in the shared warm route pool, so the aux-leg path
finds a full set rather than whatever one earlier dial happened to leave there.

## The batched request

`routing.BidirectionalRouteBatch` carries up to `routing.MaxBatchRoutes` (32)
routes that share one source and one destination.
`SetupRPCGateway.DialRouteGroupBatch` answers with one `BatchRouteResult` per
route; partial success is normal and is not an RPC error.

`CreateRouteGroupBatch` builds ONE `IDReserver` over every member's forward and
reverse hop lists, so each distinct hop is dialed once and asked once for all
the route IDs the batch needs from it. Intermediary rules are merged per hop, so
members sharing an intermediate collapse into one `AddIntermediaryRules` call.
Destination edge rules stay per member (there is no merged `AddEdgeRules` on a
visor's router RPC) but go out concurrently over the one shared connection.

For eight two-hop routes to one exit over eight distinct intermediates:

| | unbatched | batched |
|---|---|---|
| setup-node requests | 8 | 1 |
| id reservations | 24 (source and destination 8× each) | 10 |
| intermediary installs | 8 | 8 |
| destination edge installs | 8 | 8 |
| **total RPCs** | **48** | **27** |

Members that share an intermediate save more: three routes through one
intermediate cost one reservation and one rule install there, not three of each.

## Negotiation, not a flag

`SetupRPCGateway.Capabilities` (and `HealthCheck.caps`) advertise
`batch-route-setup`. The client asks once per connection; a node that predates
the RPC answers "can't find method", which reads as an empty capability list, and
the whole group falls back to today's single requests — concurrently. Nothing is
configured on either side and a mixed fleet works.

The live path today is the **legacy** one: `routing.enable_cascade_route_setup`
defaults off, so `NewSetupNodeDialerFull` is built with `forceLegacy=true`, no
source-side cascade builder exists, and every dial goes through
`SetupClient.DialRouteGroup`. The source-driven cascade is deliberately not
batched: its work is the source's, not the setup node's, so there are no per-hop
RPCs to coalesce.

## Collecting the siblings

`pkg/router/setup_batch_client.go` coalesces at the dialer rather than adding a
new multi-route entry point. Concurrent dials to one exit are parked for
`setup.batch_window` (40 ms) and leave as one request, so every existing
multi-route caller is batched without knowing about batching and `DialRoutes`
is untouched. The window is paid by the first dial of a burst, against a setup
round trip whose measured p50 is 0.6–2.1 s.

The standby pool now dials `setup.fill_inflight` (8) at a time instead of one per
tick — which is also what gives the coalescer anything to collect — with a
default ceiling of 32 (`skyenv.SkysocksClientStandbyPool`, `pool.size`). Past
`setup.first_hop_filter_max` (8) held first hops, first-hop diversity becomes a
ranking term rather than a filter: a distinct intermediate is still required, but
the twentieth tunnel is no longer refused for reusing the third's first hop.

## Telemetry

Setup-node `/stats` gains `requests_by_kind` / `routes_by_kind` (single, batch,
cascade_sign) and `batch` (batches, routes, installed, `routes_per_batch`
histogram, `per_hop_rpcs_saved`). All aggregate, so all public.

The structured keys are no longer empty for ordinary operators. `/stats` served
`"top_destinations": null` and `"recent_failures": null` to anyone off the
seven-key survey whitelist, so `pk`, `src_pk` and `dst_pk` read as empty for
every visor that might have used them. A non-whitelisted caller is now served its
OWN rows — the destinations it set up routes to and its own failures, which tell
it nothing it did not already know — and still nobody else's.

Visor side, `visor state --select diag` gains `route_setup`: batched / singles /
fallback, the oracle query and claim counters, and the warm pool's hits.
