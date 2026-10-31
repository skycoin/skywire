# weighted-by-kind.star — dynamic distribution: branch on the
# chosen candidate's transport-kinds. Routes with stcpr+sudph
# legs get a 3:1 weighted split (stcpr first); single-kind routes
# fall through to the default round-robin.
#
# This shows the SelectRoute path setting distribution. Until
# RouteSelection.Distribution was wired through, distribution was
# BeforeDial-only and a script couldn't branch on candidate
# properties. Now both phases can contribute:
#   - BeforeDial: set a baseline mux + a default distribution.
#   - SelectRoute: see the candidate, override distribution if a
#     better strategy applies.
#
# The router prefers SelectRoute's distribution over BeforeDial's
# when both are set (SelectRoute fires later in the flow).
#
# Two-phase invocation:
#   - BeforeDial (candidates empty): opt into mux=2 with a
#     conservative default ("auto"). The router falls back to
#     this when SelectRoute leaves Mode==DistributionUnset.
#   - SelectRoute (candidates populated): pick the first
#     candidate; if it has stcpr+sudph kinds, override
#     distribution to weighted 3:1.

def decide_route(ctx, candidates):
    if not candidates:
        return RouteSpec(mux=2, distribution="auto")

    c = candidates[0]
    if "stcpr" in c.transport_kinds and "sudph" in c.transport_kinds:
        return RouteSpec(chosen=c, distribution="weighted: 3, 1")
    # Single-kind: leave distribution unset → BeforeDial's "auto"
    # stays in effect.
    return RouteSpec(chosen=c)
