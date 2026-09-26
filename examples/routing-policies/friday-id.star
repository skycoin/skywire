# friday-id.star — example routing policy from RFC #2882.
#
# At Friday evening (5pm operator-local), only routes that transit
# through Indonesia (ISO-3166 "ID") are eligible. Other times, the
# lowest-latency candidate wins. Apps `vpn-client` and `vpn-server`
# get mux=4; everything else gets mux=1.
#
# Two-phase invocation:
#   - BeforeDial (candidates empty): set per-app mux only.
#   - SelectRoute (candidates populated): filter by geo when it's
#     Friday 17:00, pick lowest-latency survivor.
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

def _per_app_mux(app):
    return 4 if app in ("vpn-client", "vpn-server") else 1

def decide_route(ctx, candidates):
    # BeforeDial: ctx-only knobs (mux, min_hops).
    if not candidates:
        return RouteSpec(mux=_per_app_mux(ctx.app), min_hops=2)

    t = ctx.now()
    is_friday_evening = (
        datetime.weekday(t) == "friday"
        and t.hour == 17
    )

    # Filter step: only the Friday-evening rule narrows the
    # candidate set. Other times, every candidate stays.
    if is_friday_evening:
        candidates = [c for c in candidates if "ID" in c.hops_geo]

    # No candidates survived the Friday filter → drop. (The
    # fallback="direct" path documented in the RouteSpec type
    # is not honored by the router today: only "drop" causes
    # refusal; "direct" silently falls through to the router's
    # built-in pick on the UNFILTERED candidate list, which
    # would defeat the policy. Use "drop" so the operator
    # actually gets what they asked for.)
    if not candidates:
        return RouteSpec(fallback="drop")

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

    return RouteSpec(chosen=chosen)
