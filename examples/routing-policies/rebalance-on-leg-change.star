# rebalance-on-leg-change.star — keep an even-weighted schedule
# in sync with the live leg count. Useful when an operator wants
# equal split across however many mux legs end up active, as legs
# come and go after initial setup.
#
# Three phases:
#   - BeforeDial: set mux baseline.
#   - SelectRoute: pick the first candidate; the router's mux loop
#     adds more.
#   - on_leg_change (RFC #2882 phase 6): the route group fires
#     this whenever a leg is added (mux loop appended an aux
#     route, or appendRouteToGroup ran) or dropped (transport
#     close). Return a fresh distribution descriptor sized to the
#     current live leg count.
#
# Only `distribution` is honored from the on_leg_change return —
# mux/min_hops/chosen are dial-time decisions that can't change
# after setup. Failure / parse-error / script panic in
# on_leg_change is non-fatal: the previous distribution stays in
# effect and the leg change is logged but not undone.

def decide_route(ctx, candidates):
    if not candidates:
        return RouteSpec(mux=4, distribution="auto")
    return RouteSpec(chosen=candidates[0])

def on_leg_change(ctx, legs, change):
    # Count live legs and emit an equal-weight schedule sized
    # to match. Empty schedule (no live legs) → no override.
    n = 0
    for l in legs:
        if l.alive:
            n += 1
    if n == 0:
        return RouteSpec()
    parts = []
    for i in range(n):
        parts.append("1")
    return RouteSpec(distribution = "weighted: " + ", ".join(parts))
