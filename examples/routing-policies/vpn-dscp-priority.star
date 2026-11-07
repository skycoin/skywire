# vpn-dscp-priority.star — for VPN traffic, route by IPv4 DSCP:
# packets marked Expedited Forwarding (DSCP=46 / EF — typical
# VoIP) take leg 0 (the low-latency leg); everything else
# round-robins across the other legs.
#
# Layer-2 descriptor: "dscp-priority: N" — reads the upper 6
# bits of the ToS byte (offset 1) in the IPv4 header. Threshold
# in [1, 63]; common values:
#   46 — EF, Expedited Forwarding (VoIP, real-time)
#   18 — AF21, Assured Forwarding (interactive)
#   10 — AF11, lower-priority assured
#
# Non-IPv4 payloads (rare in VPN traffic but possible for
# control packets) fall back to round-robin without taking leg 0.
#
# Pair with sticky:5tuple in a separate route group for the
# bulk traffic if the operator wants flow affinity on the
# non-prio side; today distribution is one-per-route-group.

def decide_route(ctx, candidates):
    if ctx.app not in ("vpn-client", "vpn-server"):
        return RouteSpec()
    if not candidates:
        return RouteSpec(mux=3, distribution="dscp-priority: 46")
    return RouteSpec(chosen=candidates[0])
