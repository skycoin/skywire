# rt-latency-budget.star — real-time apps need <200ms; other apps
# take whatever's available.
#
# Two-phase invocation:
#   - BeforeDial (candidates empty): for rt apps, opt into mux=2
#     for redundancy against single-leg jitter.
#   - SelectRoute (candidates populated): keep candidates under
#     the 200ms budget, drop the dial if nothing qualifies (or
#     fall back to direct — operator tunes the strictness here).

def decide_route(ctx, candidates):
    if ctx.app not in ("rt-voice", "rt-video"):
        return RouteSpec()

    # BeforeDial: just set mux=2 (defer latency filter).
    if not candidates:
        return RouteSpec(mux=2)

    # SelectRoute: keep candidates under the 200ms budget.
    # Starlark doesn't support Python-style chained comparisons.
    fast = [c for c in candidates if c.est_latency_ms > 0 and c.est_latency_ms < 200]
    if not fast:
        # logging.{info,warn} take exactly one string argument;
        # build the message with concatenation.
        logging.warn(
            "rt: no route under 200ms budget for app=" + ctx.app
            + " peer=" + ctx.peer_pk,
        )
        # No route fits the budget — drop. (The fallback="direct"
        # path isn't honored by the router today; only "drop"
        # actually refuses the dial. See README "two-phase
        # invocation model" for the full story.)
        return RouteSpec(fallback="drop")

    chosen = fast[0]
    for c in fast[1:]:
        if c.est_latency_ms < chosen.est_latency_ms:
            chosen = c
    return RouteSpec(chosen=chosen)
