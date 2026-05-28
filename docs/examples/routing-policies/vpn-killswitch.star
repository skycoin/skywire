# vpn-killswitch.star — VPN traffic MUST egress through direct-IP
# transports; if no such route exists, drop rather than leak
# through dmsg.
#
# Rationale: dmsg goes through a public relay, which defeats
# the purpose of running a VPN over skywire for users who care
# about hiding their traffic from the relay operator. The
# killswitch makes that explicit: no direct-IP route → no dial.

def decide_route(ctx, candidates):
    if ctx.app != "vpn-client":
        return RouteSpec()

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
    return RouteSpec(chosen=direct[0], mux=2)
