# vpn-bulk-split.star — for VPN traffic, send MTU-sized packets
# down leg 0 (the wide pipe — the first leg the route-finder
# returned, ranked by latency) and round-robin small/control
# packets across the rest.
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
    if not candidates:
        return RouteSpec()
    return RouteSpec(
        chosen=candidates[0],
        mux=2,
        distribution="size-threshold: 1400",
    )
