# no-us-transit.star — refuse routes that pass through US intermediates.
#
# `c.hops_geo` is a parallel list to `c.hops`; "??" means unknown
# (no SD entry, no direct transport to that intermediate). This
# example treats unknown as "not US" — the operator could choose
# the stricter "unknown means drop" by filtering out "??" too.
#
# When every candidate touches US, drop loudly (fallback="drop")
# rather than silently routing through it: the operator notices
# their workload is offline and can update the policy.

def decide_route(ctx, candidates):
    filtered = [c for c in candidates if "US" not in c.hops_geo]
    if not filtered:
        return RouteSpec(fallback="drop")
    # Lowest-latency among survivors. Zero latency = unknown,
    # so we don't pick a "zero" candidate over a measured one.
    chosen = filtered[0]
    for c in filtered[1:]:
        if c.est_latency_ms > 0 and (chosen.est_latency_ms == 0 or c.est_latency_ms < chosen.est_latency_ms):
            chosen = c
    return RouteSpec(chosen=chosen)
