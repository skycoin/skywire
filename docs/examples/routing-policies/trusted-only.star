# trusted-only.star — every intermediate must be on the operator's
# trust list (Pty.Whitelist ∪ Hypervisors).
#
# Two-phase invocation:
#   - BeforeDial (candidates empty): nothing to filter on.
#   - SelectRoute (candidates populated): keep candidates where
#     every hop is trusted; drop if none survives.
#
# Beware: this is restrictive. With a small trust set, most peers
# are unreachable. The "drop" fallback surfaces that rather than
# pretending the dial succeeded.

def decide_route(ctx, candidates):
    # BeforeDial: defer.
    if not candidates:
        return RouteSpec()

    safe = []
    for c in candidates:
        ok = True
        for h in c.hops:
            if not peers.is_trusted(h):
                ok = False
                break
        if ok:
            safe.append(c)
    if not safe:
        # logging.{info,warn} take exactly one string argument.
        logging.warn(
            "trusted-only: no candidate route is fully trusted for peer="
            + ctx.peer_pk,
        )
        return RouteSpec(fallback="drop")
    return RouteSpec(chosen=safe[0])
