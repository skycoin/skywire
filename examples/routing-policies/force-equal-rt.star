# force-equal-rt.star — force round-robin distribution for a
# real-time app even when the visor's global muxMode is the
# latency-weighted default.
#
# Use case: under bursty conditions, the latency-weighted
# selector can briefly concentrate traffic on one leg if that
# leg's EMA dropped low, increasing jitter for rt traffic. Equal
# RR trades a small throughput penalty (the slow leg gets the
# same packets/sec) for predictable inter-packet timing.

def decide_route(ctx, candidates):
    if ctx.app in ("rt-voice", "rt-video"):
        return RouteSpec(mux=2, distribution="round-robin")
    return RouteSpec()
