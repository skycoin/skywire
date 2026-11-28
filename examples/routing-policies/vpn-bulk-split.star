# vpn-bulk-split.star — for VPN traffic, send MTU-sized packets
# down leg 0 (the wide pipe — first leg the route-finder returned,
# ranked by latency) and round-robin small/control packets across
# the rest.
#
# Two-phase invocation:
#   - BeforeDial (candidates empty): set the mux + distribution
#     knobs. These take effect only when set in BeforeDial — the
#     SelectRoute hook only honors `chosen` and `drop`.
#   - SelectRoute (candidates populated): just return the first
#     candidate as chosen; the actual leg-by-size routing is
#     handled by the transport selector once mux is established.
#
# Layer-2 descriptor: "size-threshold: N" — payloads strictly
# greater than N bytes go to leg 0; payloads <= N round-robin
# across legs 1..N-1. Packets without a known size (handshake,
# control) take leg 0 as a safe default.
#
# 1400 is the common Ethernet-MTU-minus-IP-overhead boundary.

def decide_route(ctx, candidates):
    if ctx.app != "vpn-client":
        return RouteSpec()

    # BeforeDial: set the knobs.
    if not candidates:
        return RouteSpec(mux=2, distribution="size-threshold: 1400")

    # SelectRoute: just pick the first candidate; the router's
    # disjoint-mux loop establishes the additional legs.
    return RouteSpec(chosen=candidates[0])
