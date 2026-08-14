# low-latency-direct — one shortest, lowest-latency path; no fan-out, no churn.
#
# For interactive / round-trip-bound traffic (RPC, remote shell, chat, game-like
# flows) where latency matters far more than throughput or path diversity. A
# single leg at min_hops 1 takes the direct transport where one exists, and the
# latency-weighted route-finder picks the fastest candidate. Rotation is off — an
# interactive session wants a stable path, not periodic re-routing.
#
# Trade-off: no privacy fan-out (a single relay, or none, sees the flow) and no
# redundancy — if the one path dies the connection re-dials. Use spread-bw or
# privacy-max instead when unlinkability or resilience matters more than RTT.
#
#   "routing": { "policy_per_dial": "preset:low-latency-direct" }

def decide_route(ctx, candidates):
    return RouteSpec(
        mux=1,
        min_hops=1,
        distribution="auto",
        rotation_interval_seconds=0,   # 0 = no scheduled rotation; keep the path stable
    )
