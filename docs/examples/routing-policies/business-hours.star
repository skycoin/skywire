# business-hours.star — during 9-17 weekdays, only routes through
# SG/JP/ID survive; after hours, anything goes.
#
# Time is read from ctx.now() which is wall-clock at the moment
# the dial fires. Override via the policy evaluator's Clock for
# tests (see pkg/router/policy/evaluator_test.go).

def decide_route(ctx, candidates):
    t = ctx.now()
    day = datetime.weekday(t)
    # Starlark doesn't support Python-style chained comparisons
    # (9 <= h < 17). Split into two and-ed clauses.
    is_business = day not in ("saturday", "sunday") and t.hour >= 9 and t.hour < 17

    if is_business:
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

    # Off-hours: visor default.
    return RouteSpec()
