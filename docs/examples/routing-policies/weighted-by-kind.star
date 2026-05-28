# weighted-by-kind.star — when a route has two mux legs of mixed
# transport kinds (e.g. stcpr + dmsg), give the direct-IP leg more
# of the per-packet share than the relayed dmsg leg.
#
# Layer-2 descriptor: "weighted: f1, f2, ..., fN" — fractional
# weights normalized to an integer schedule by the router's
# transport selector. Length must match mux.

def decide_route(ctx, candidates):
    if not candidates:
        return RouteSpec(mux=2)
    chosen = candidates[0]
    # Heuristic: stcpr + dmsg present → 3:1 in favor of stcpr.
    if "stcpr" in chosen.transport_kinds and "dmsg" in chosen.transport_kinds:
        return RouteSpec(chosen=chosen, mux=2, distribution="weighted: 3, 1")
    # Default: round-robin (the empty "" descriptor leaves it
    # to the visor-wide muxMode, which is latency-weighted).
    return RouteSpec(chosen=chosen, mux=2)
