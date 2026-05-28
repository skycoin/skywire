# friday-id.star — example routing policy from RFC #2882.
#
# At Friday evening (5pm operator-local), only routes that transit
# through Indonesia (ISO-3166 "ID") are eligible. Other times, the
# lowest-latency candidate wins. Apps `vpn-client` and `vpn-server`
# get mux=4; everything else gets mux=1.
#
# Operator workflow:
#   sudo install -m 0644 friday-id.star /etc/skywire/policies/
#   # in skywire.json: "routing": {
#   #   "policy_per_dial": "@/etc/skywire/policies/friday-id.star"
#   # }
#   sudo systemctl reload skywire
#
# Preview a decision without dialing:
#   skywire cli route policy test \
#     --script /etc/skywire/policies/friday-id.star \
#     --dial '{"app":"skychat","now":"2026-05-29T17:00:00Z"}'

def decide_route(ctx, candidates):
    t = ctx.now()
    is_friday_evening = (
        datetime.weekday(t) == "friday"
        and t.hour == 17
    )

    # Filter step: only the Friday-evening rule narrows the
    # candidate set. Other times, every candidate stays.
    if is_friday_evening:
        candidates = [c for c in candidates if "ID" in c.hops_geo]

    # No candidates survived → fall back to a direct dial. The
    # visor falls back to the built-in default when the script
    # returns Fallback="" or None, so we use "direct" here only
    # because we specifically want a direct transport instead.
    if not candidates:
        return RouteSpec(fallback="direct")

    # Pick the lowest-latency surviving candidate. Zero latency
    # means "unknown" (no recent measurement) — we treat it as
    # the worst case so it doesn't shadow a real measurement.
    chosen = candidates[0]
    for c in candidates[1:]:
        if c.est_latency_ms > 0 and (
            chosen.est_latency_ms == 0
            or c.est_latency_ms < chosen.est_latency_ms
        ):
            chosen = c

    return RouteSpec(
        chosen = chosen,
        mux = 4 if ctx.app in ("vpn-client", "vpn-server") else 1,
        min_hops = 2,
    )
