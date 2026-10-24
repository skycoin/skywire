# asymmetric-stcpr-sudph.star — different transport kinds for
# forward vs reverse. Forward (upstream) uses stcpr (TCP, lower
# loss, slower); reverse (downstream) uses sudph (UDP-hole-punched,
# better for bulk throughput).
#
# Two-phase invocation:
#   - BeforeDial (candidates empty): set per-direction mux + min
#     hops asymmetrically. This is the new forward_mux / reverse_mux
#     pair — Mux (symmetric) still works but is overridden when
#     forward_mux or reverse_mux is set.
#   - SelectRoute (candidates populated): pick forward by stcpr,
#     reverse by sudph. ctx.reverse_candidates exposes the reverse-
#     direction candidate list (independent from `candidates`,
#     which is always the forward list).
#
# Note: forward and reverse candidates are often the SAME list when
# the route-finder query was symmetric (same MinHops for both
# directions). The router only splits into separate queries when
# the script asks for asymmetric MinHops up front.

def _first_with_kind(cs, kind):
    for c in cs:
        if kind in c.transport_kinds:
            return c
    return None

def decide_route(ctx, candidates):
    # BeforeDial: 1 forward leg, 4 reverse legs — typical
    # download-heavy workload. Forward needs only 1 stcpr; reverse
    # gets 4 sudph for aggregated bulk.
    if not candidates:
        return RouteSpec(
            forward_mux=1, reverse_mux=4,
            forward_min_hops=2, reverse_min_hops=2,
        )

    # SelectRoute: pick by kind per direction.
    fwd = _first_with_kind(candidates, "stcpr")
    rev = _first_with_kind(ctx.reverse_candidates, "sudph")

    if fwd == None and rev == None:
        # Neither direction has a matching candidate — drop so
        # the operator sees the policy isn't satisfiable for
        # this dial.
        return RouteSpec(fallback="drop")

    # Partial picks are fine: the router will fill in the unset
    # direction via its built-in latency-ranked pick.
    return RouteSpec(chosen=fwd, reverse_chosen=rev)
