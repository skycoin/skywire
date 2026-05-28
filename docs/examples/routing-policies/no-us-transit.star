# no-us-transit.star — refuse routes that pass through US
# intermediates.
#
# Two-phase invocation:
#   - BeforeDial (candidates empty): nothing to filter yet.
#   - SelectRoute (candidates populated): filter on hops_geo,
#     drop if every candidate touches US.
#
# `c.hops_geo` is a parallel list to `c.hops`; "??" means unknown
# (no SD entry, no direct transport to that intermediate). This
# example treats unknown as "not US" — the operator could choose
# the stricter "unknown means drop" by filtering out "??" too.

def decide_route(ctx, candidates):
    # BeforeDial: nothing to filter on yet.
    if not candidates:
        return RouteSpec()

    # SelectRoute: drop any candidate that touches US.
    filtered = [c for c in candidates if "US" not in c.hops_geo]
    if not filtered:
        # Every candidate touched US — drop loudly rather than
        # silently routing through; the operator notices the
        # workload is offline and can update the policy.
        return RouteSpec(fallback="drop")
    # Lowest-latency among survivors. Zero latency = unknown,
    # so we don't pick a "zero" candidate over a measured one.
    chosen = filtered[0]
    for c in filtered[1:]:
        if c.est_latency_ms > 0 and (chosen.est_latency_ms == 0 or c.est_latency_ms < chosen.est_latency_ms):
            chosen = c
    return RouteSpec(chosen=chosen)
