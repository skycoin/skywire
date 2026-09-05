# app-mux.star — different apps get different fanout.
#
# The simplest useful Layer-1 policy: branch on ctx.app to set
# per-app mux + min_hops. Latency-sensitive apps stay single-route;
# bandwidth apps get parallel legs.
#
# Install:
#   sudo install -m 0644 app-mux.star /etc/skywire/policies/
#   # in skywire.json:
#   #   "routing": { "policy_per_dial": "@/etc/skywire/policies/app-mux.star" }
#
# Preview:
#   skywire cli route policy test --script app-mux.star \
#     --dial '{"app":"vpn-client"}'

def decide_route(ctx, candidates):
    if ctx.app == "vpn-client":
        return RouteSpec(mux=4, min_hops=2)
    if ctx.app == "skychat":
        # Chat is latency-sensitive — one route, lowest mux.
        return RouteSpec(mux=1)
    # Everything else: visor defaults.
    return RouteSpec()
