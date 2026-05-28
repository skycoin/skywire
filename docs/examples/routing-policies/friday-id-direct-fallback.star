# friday-id-direct-fallback.star — Friday 17:00 → Indonesia-transit
# only for multihop, otherwise fall back to a DIRECT transport
# instead of dropping the dial.
#
# Differs from friday-id.star: when the Friday filter wipes the
# candidate list, this script uses fallback="direct" — the router
# forces UseExistingTpOnly=true + MinHops=1, taking the direct-
# transport short-circuit if one exists, dropping otherwise. The
# operator gets connectivity when there's no overlay route that
# satisfies the policy.
#
# Use case: a strict geo policy that still allows the operator to
# reach peers they have a direct transport to (peers in their own
# trust circle), without bouncing through Indonesia-via-multihop.

def decide_route(ctx, candidates):
    # BeforeDial: defer to SelectRoute.
    if not candidates:
        return RouteSpec()

    t = ctx.now()
    if datetime.weekday(t) == "friday" and t.hour == 17:
        candidates = [c for c in candidates if "ID" in c.hops_geo]
    if not candidates:
        # No multihop survived — try direct transport instead of
        # dropping. If the peer has no direct transport, the dial
        # fails with the same ErrDialPolicyDropped that "drop"
        # would have produced, but most peer relationships have
        # a direct transport so this is usually graceful.
        return RouteSpec(fallback="direct")

    chosen = candidates[0]
    for c in candidates[1:]:
        if c.est_latency_ms > 0 and (chosen.est_latency_ms == 0 or c.est_latency_ms < chosen.est_latency_ms):
            chosen = c
    return RouteSpec(chosen=chosen)
