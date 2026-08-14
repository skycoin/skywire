# prefer-connected — reuse the transports we already hold; don't pay to set up a
# fresh one on the dial hot path.
#
# Route-setup cost is dominated by building NEW transports (noise handshake + AR
# register + route-finder lookup). When the visor already holds a live transport
# to the target, the cheapest, fastest, lowest-latency route is the one that
# rides it. This preset asks the provider whether such a transport exists
# (peers.has_transport) and shapes the route accordingly:
#
#   already connected  → mux=1, min_hops=1  → take the direct transport we hold;
#                        no fan-out, no churn — there is nothing new to set up.
#   not connected      → mux=2, min_hops=2  → route through intermediates (which
#                        are frequently peers we ARE already connected to)
#                        instead of forcing a brand-new direct transport to a
#                        stranger; a light fan-out keeps some throughput +
#                        reward spread while paths warm up.
#
# This is the routing-layer companion to proxy/exit selection preferring servers
# we already have transports to: there the app picks a connected exit; here the
# router, given whatever target, prefers the path that reuses existing plumbing.
# Good default for a visor that already maintains a healthy transport set and
# wants dials to be fast and cheap rather than maximally private.
#
# Select without compiling a file:
#   "routing": { "policy_per_dial": "preset:prefer-connected" }

CONNECTED_MUX = 1
CONNECTED_MIN_HOPS = 1
COLD_MUX = 2
COLD_MIN_HOPS = 2
COLD_ROTATE_SECS = 30   # only the cold (multihop) path rotates, to spread relays

def decide_route(ctx, candidates):
    if peers.has_transport(ctx.peer_pk):
        return RouteSpec(
            mux=CONNECTED_MUX,
            min_hops=CONNECTED_MIN_HOPS,
            distribution="auto",
            rotation_interval_seconds=0,   # stable: reuse the transport we hold
        )
    return RouteSpec(
        mux=COLD_MUX,
        min_hops=COLD_MIN_HOPS,
        distribution="auto",
        rotation_interval_seconds=COLD_ROTATE_SECS,
    )
