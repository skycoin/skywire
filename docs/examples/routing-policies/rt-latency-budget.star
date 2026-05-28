# rt-latency-budget.star — real-time apps need <200ms; other apps
# take whatever's available.
#
# `c.est_latency_ms` is the sum of the route's per-hop latencies
# from the router's existing transport-tracker lookup. Zero means
# no measurement available — we treat it as "unknown" rather than
# "fast" so we don't accidentally pick an unmeasured route over a
# measured one.

def decide_route(ctx, candidates):
    if ctx.app not in ("rt-voice", "rt-video"):
        return RouteSpec()

    # Starlark doesn't support Python-style chained comparisons.
    fast = [c for c in candidates if c.est_latency_ms > 0 and c.est_latency_ms < 200]
    if not fast:
        # logging.{info,warn} take exactly one string argument;
        # build the message with concatenation.
        logging.warn(
            "rt: no route under 200ms budget for app=" + ctx.app
            + " peer=" + ctx.peer_pk,
        )
        # Try a direct dial instead of failing. The operator
        # tunes this — "drop" for stricter latency SLOs.
        return RouteSpec(fallback="direct")

    chosen = fast[0]
    for c in fast[1:]:
        if c.est_latency_ms < chosen.est_latency_ms:
            chosen = c
    # mux=2 for redundancy against single-leg jitter, which
    # matters more on rt traffic than throughput.
    return RouteSpec(chosen=chosen, mux=2)
