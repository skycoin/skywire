# weighted-by-kind.star — static weighted distribution for the
# operator's bandwidth-asymmetric apps. The first mux leg gets 3x
# the per-packet share of the second, matching the typical case
# where the route-finder's first-pick is the lower-latency path
# and we want most traffic on it but still keep the second as
# active redundancy.
#
# Two-phase invocation:
#   - BeforeDial (candidates empty): set mux + distribution. These
#     are the only knobs the router honors for distribution; both
#     must be set here (SelectRoute only propagates `chosen` and
#     `drop`).
#   - SelectRoute (candidates populated): pick the first
#     candidate; the disjoint-mux loop establishes additional legs
#     and the selector applies the 3:1 schedule.
#
# Weights are operator fractional ratios — "3, 1" gives the first
# leg 75% of packets, the second 25%. The selector normalizes to
# an integer schedule via round-to-nearest.
#
# Suitable for apps where you want bulk-traffic concentration on
# the better leg but mux=1 isn't an option (need the redundancy).
# Picky real-time apps want force-equal-rt.star instead; latency-
# sensitive small-message apps want size-threshold.

def decide_route(ctx, candidates):
    # BeforeDial: set mux + the 3:1 weighted schedule.
    if not candidates:
        return RouteSpec(mux=2, distribution="weighted: 3, 1")

    # SelectRoute: take the first candidate.
    return RouteSpec(chosen=candidates[0])
