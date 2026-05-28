# friday-id-mux.star — combines the RFC's headline Indonesia-Friday
# Layer-1 example with the Layer-2 size-threshold descriptor for
# vpn-client.
#
# On Friday 17:00 local time, only Indonesia-transit routes
# survive. The lowest-latency survivor wins. VPN traffic also
# splits by packet size; everything else gets default RR.

def decide_route(ctx, candidates):
    t = ctx.now()
    if datetime.weekday(t) == "friday" and t.hour == 17:
        candidates = [c for c in candidates if "ID" in c.hops_geo]
    if not candidates:
        return RouteSpec(fallback="direct")

    chosen = candidates[0]
    for c in candidates[1:]:
        if c.est_latency_ms > 0 and (chosen.est_latency_ms == 0 or c.est_latency_ms < chosen.est_latency_ms):
            chosen = c

    if ctx.app == "vpn-client":
        return RouteSpec(chosen=chosen, mux=4, distribution="size-threshold: 1400")
    return RouteSpec(chosen=chosen, mux=1)
