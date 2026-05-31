# business-hours.star — during 9-17 weekdays, only routes through
# SG/JP/ID survive; after hours, anything goes.
#
# Two-phase invocation:
#   - BeforeDial (candidates is empty): set per-dial knobs only.
#     This script has no ctx-only knobs to set, so it returns the
#     bare RouteSpec() and defers everything to SelectRoute.
#   - SelectRoute (candidates populated): filter by geo, return
#     the first survivor as chosen — or drop if the filter wiped
#     the candidate list during business hours.
#
# Time is read from ctx.now() which is wall-clock at the moment
# the dial fires.

def decide_route(ctx, candidates):
    # BeforeDial: nothing to override.
    if not candidates:
        return RouteSpec()

    t = ctx.now()
    day = datetime.weekday(t)
    # Starlark doesn't support Python-style chained comparisons
    # (9 <= h < 17); split into two and-ed clauses.
    is_business = day not in ("saturday", "sunday") and t.hour >= 9 and t.hour < 17

    if not is_business:
        # Off-hours: any candidate is fine; defer the pick to the
        # router's disjoint-path latency rank by returning bare.
        return RouteSpec()

    ok = ("SG", "JP", "ID")
    filtered = []
    for c in candidates:
        for g in c.hops_geo:
            if g in ok:
                filtered.append(c)
                break
    if not filtered:
        return RouteSpec(fallback="drop")
    return RouteSpec(chosen=filtered[0])
