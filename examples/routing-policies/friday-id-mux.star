# friday-id-mux.star — combines the RFC's headline Indonesia-Friday
# Layer-1 example with the Layer-2 size-threshold descriptor for
# vpn-client.
#
# Two-phase invocation:
#   - BeforeDial (candidates empty): set per-app mux + the
#     distribution descriptor. These knobs must be set here, NOT
#     in SelectRoute (SelectRoute only honors `chosen` and `drop`).
#   - SelectRoute (candidates populated): filter on Friday-17:00
#     Indonesia constraint, then return the lowest-latency
#     survivor as chosen.

def decide_route(ctx, candidates):
    # BeforeDial: mux + distribution knobs.
    if not candidates:
        if ctx.app == "vpn-client":
            return RouteSpec(mux=4, distribution="size-threshold: 1400")
        return RouteSpec(mux=1)

    t = ctx.now()
    if datetime.weekday(t) == "friday" and t.hour == 17:
        candidates = [c for c in candidates if "ID" in c.hops_geo]
    # See friday-id.star: "direct" isn't honored today, use "drop".
    if not candidates:
        return RouteSpec(fallback="drop")

    chosen = candidates[0]
    for c in candidates[1:]:
        if c.est_latency_ms > 0 and (chosen.est_latency_ms == 0 or c.est_latency_ms < chosen.est_latency_ms):
            chosen = c
    return RouteSpec(chosen=chosen)
