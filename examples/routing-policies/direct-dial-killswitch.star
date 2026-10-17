# direct-dial-killswitch.star — VPN apps may never use a DMSG
# direct transport. The default vpn-killswitch.star covers the
# overlay (DialRoutes) path; this one covers the direct
# (sky_forward_conn / VStreamMux) short-circuit that bypasses
# the route-finder when an existing direct transport to the
# peer exists. Without it, a VPN dial would happily use a DMSG
# direct transport even when the operator's overlay policy
# would have refused it.
#
# How direct dials surface:
#   - ctx.is_direct_dial = True
#   - ctx.transport_kind = "stcpr" / "sudph" / "stcp" / "dmsg"
#   - candidates is empty (no overlay candidates exist for
#     a direct dial)
#
# Returning fallback="drop" causes VStreamMux.Dial to fail with
# ErrDialPolicyDropped, which the app sees as a normal dial
# error. Anything else allows the dial.
#
# Other RouteSpec fields (mux / min_hops / distribution) are
# no-ops on direct dials — there's a single transport, no
# routing decisions to make beyond accept/refuse.

def decide_route(ctx, candidates):
    if ctx.is_direct_dial:
        # Direct path: VPN apps refuse DMSG; everything else passes.
        if ctx.app in ("vpn-client", "vpn-server"):
            if ctx.transport_kind == "dmsg":
                logging.warn("vpn killswitch (direct): refusing DMSG transport")
                return RouteSpec(fallback="drop")
        return RouteSpec()

    # Overlay path (the existing vpn-killswitch logic applies
    # here — see vpn-killswitch.star for the canonical version).
    return RouteSpec()
