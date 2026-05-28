# weighted-by-kind.star — operator-supplied per-leg fractional
# weights for a mux route, applied when the route's transport
# kinds aren't all the same. Useful when one leg is stcpr (direct
# IP, faster) and the other is sudph (UDP-hole-punched, also
# direct but can have different congestion behavior).
#
# Layer-2 descriptor: "weighted: f1, f2, ..., fN" — fractional
# weights normalized to an integer schedule by the router's
# transport selector. Length must match mux.
#
# NOTE: DMSG transports never appear in multihop / mux routes —
# the router filters them out at route construction time because
# a dmsg server is an unaccountable intermediary (potentially
# chained via server-to-server forwarding) that the route's
# endpoints can't observe. So no policy needs to special-case
# DMSG-in-mux; it can't happen.

def decide_route(ctx, candidates):
    if not candidates:
        return RouteSpec(mux=2)
    chosen = candidates[0]
    # Heuristic: when the chosen route mixes stcpr and sudph,
    # weight stcpr 3:1 (TCP-based, generally lower jitter than
    # UDP hole-punched). Operators tune the ratio to their links.
    if "stcpr" in chosen.transport_kinds and "sudph" in chosen.transport_kinds:
        return RouteSpec(chosen=chosen, mux=2, distribution="weighted: 3, 1")
    # Default: round-robin (the empty "" descriptor leaves it
    # to the visor-wide muxMode, which is latency-weighted).
    return RouteSpec(chosen=chosen, mux=2)
