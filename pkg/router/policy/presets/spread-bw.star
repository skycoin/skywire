# spread-bw — benevolent bandwidth spreading (the "browse benevolently" preset).
#
# Routes app traffic (browser/Telegram/VPN over skysocks-client, etc.) across
# parallel MULTIHOP routes and continuously maintains the mux: it sheds the
# single worst leg per tick — lossy (high SACK retransmits) or slow (high RTT)
# first, otherwise one that has carried its byte budget — and replaces it with a
# fresh leg through different intermediates. Because min_hops >= 2 forces every
# leg through real intermediate visors (which earn bandwidth-reward credit for
# relaying), ordinary browsing through this policy distributes a reward-eligible
# bandwidth floor across many network visors. Dynamic routing, not a static
# circuit.
#
# Resilience: only ONE leg is retired per tick and never the last live leg, so
# the remaining legs keep serving — no simultaneous drop-all that stalls the
# connection. Lossy/slow legs are scored by latency + a heavy per-retransmit
# penalty and shed only when clearly worse than the healthiest leg, so healthy
# legs aren't churned.
#
# Select it without compiling or shipping a file:
#   "routing": { "policy_per_dial": "preset:spread-bw" }

MUX = 4               # parallel legs — spread across MUX intermediates at once
MIN_HOPS = 2          # >= 2 forces real intermediates (excludes the direct transport)
ROTATE_SECS = 15      # health/rotation tick cadence
LEG_BUDGET = 8000000  # retire a leg after it has carried ~8 MB, spreading over time

RETX_PENALTY_MS = 50  # each SACK retransmit adds this much "badness" (loss >> latency)
BAD_LEG_FACTOR = 2    # shed a leg only when this many times worse than the best
MIN_BAD_MS = 200      # ...and only above this absolute badness (don't churn fast legs)

def decide_route(ctx, candidates):
    return RouteSpec(
        mux=MUX,
        min_hops=MIN_HOPS,
        distribution="auto",
        rotation_interval_seconds=ROTATE_SECS,
    )

def _badness(l):
    # Lossy legs cost far more than merely slow ones.
    return l.latency_ms + l.retransmits * RETX_PENALTY_MS

def on_tick(ctx, legs):
    alive = [l for l in legs if l.alive]
    if len(alive) < 2:
        return None  # never retire our last working leg

    worst = alive[0]
    best = alive[0]
    for l in alive:
        if _badness(l) > _badness(worst):
            worst = l
        if _badness(l) < _badness(best):
            best = l

    drop = None
    if _badness(worst) > MIN_BAD_MS and _badness(worst) > _badness(best) * BAD_LEG_FACTOR:
        # 1) Shed a clearly-unhealthy (lossy or slow) leg.
        drop = worst
    else:
        # 2) Otherwise keep spreading: retire one budget-exhausted leg onto a
        #    fresh set of intermediates.
        for l in alive:
            if (l.sent_bytes + l.recv_bytes) > LEG_BUDGET:
                drop = l
                break

    if drop == None:
        return None
    # Replace it (add_leg) and steer the replacement away from this leg's
    # intermediates so we land on fresh relays.
    return Rotation(drop_legs=[drop.index], add_leg=True, exclude_hops=drop.hops)
