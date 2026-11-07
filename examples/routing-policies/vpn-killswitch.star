# vpn-killswitch.star — VPN traffic MUST egress through direct-IP
# transports (stcpr / sudph); if no such route exists, drop rather
# than fall back to relayed transports.
#
# Two-phase invocation:
#   - BeforeDial (candidates empty): set mux=2 for vpn-client so
#     the route group always negotiates mux, since direct-IP
#     routes give us multiple disjoint candidates to spread across.
#   - SelectRoute (candidates populated): for vpn-client, drop any
#     candidate that contains a non-direct-IP leg, drop the dial
#     if everything was filtered.
#
# NOTE: DMSG hops are already filtered out of multihop routes at
# the router level (see PR #2899), so c.transport_kinds for any
# multihop candidate will only contain non-DMSG types. This
# policy is still useful for refusing single-hop DMSG dials
# (direct DMSG to peer) when the operator wants VPN to never use
# the DMSG relay at all.

def decide_route(ctx, candidates):
    if ctx.app != "vpn-client":
        return RouteSpec()

    # BeforeDial: just set mux=2; defer the filter to SelectRoute.
    if not candidates:
        return RouteSpec(mux=2)

    # SelectRoute: keep candidates whose every leg is direct IP.
    direct = []
    for c in candidates:
        ok = True
        for k in c.transport_kinds:
            if k not in ("stcpr", "sudph"):
                ok = False
                break
        if ok:
            direct.append(c)
    if not direct:
        logging.warn(
            "vpn killswitch: no direct-IP route available; dropping",
        )
        return RouteSpec(fallback="drop")
    return RouteSpec(chosen=direct[0])
