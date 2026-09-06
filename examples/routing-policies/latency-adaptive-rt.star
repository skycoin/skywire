# latency-adaptive-rt.star — for real-time apps, pick the lowest-
# latency leg *per packet* (re-evaluates every packet, so a leg
# whose RTT spikes mid-session loses traffic immediately).
#
# Differs from "auto" — auto builds a latency-weighted schedule
# and rebuilds it periodically. Adaptive scans the live legs on
# every packet, so the operator's choice tracks the live state
# instantly at the cost of a small per-packet scan (linear in
# mux count, typically <=4 legs).

def decide_route(ctx, candidates):
    if ctx.app not in ("rt-voice", "rt-video"):
        return RouteSpec()
    if not candidates:
        return RouteSpec(mux=3, distribution="latency-adaptive")
    return RouteSpec(chosen=candidates[0])
