# balanced — a sensible middle-ground default: a little redundancy and a little
# privacy, without the latency of forced multihop or the overhead of a wide
# fan-out.
#
# Sits between low-latency-direct (mux=1, RTT-optimal, no privacy/redundancy) and
# spread-bw / privacy-max (wide fan-out, multihop, higher latency). Two legs give
# failover if one path dies; min_hops=1 lets the route-finder take the direct
# transport when that is genuinely the shortest path, so latency stays low for
# nearby peers while distant ones still pick up an intermediate. A gentle
# rotation refreshes paths over time to spread reward-eligible relay bandwidth
# without churning an active flow.
#
# Good "I don't have a strong preference" choice for general app traffic. Reach
# for low-latency-direct when RTT is king, spread-bw/privacy-max when throughput
# or unlinkability matters more.
#
#   "routing": { "policy_per_dial": "preset:balanced" }

def decide_route(ctx, candidates):
    return RouteSpec(
        mux=2,
        min_hops=1,
        distribution="auto",
        rotation_interval_seconds=45,
    )

def on_tick(ctx, legs):
    # Keep both legs healthy: shed a single dead leg so it re-dials onto a fresh
    # path, but never drop the last live one. No aggressive badness-shedding —
    # balanced favours a stable pair over chasing the fastest relay.
    alive = [l for l in legs if l.alive]
    dead = [l for l in legs if not l.alive]
    if len(dead) > 0 and len(alive) >= 1:
        return Rotation(drop_legs=[dead[0].index], add_leg=True, exclude_hops=dead[0].hops)
    return None
