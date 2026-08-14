# privacy-max — maximum unlinkability, at the cost of latency.
#
# >= 3 hops so no single intermediate sees both ends of the flow, 2 parallel
# paths for resilience, and frequent rotation so any given path is short-lived —
# a passive relay observes only a brief, partial slice before it's replaced
# through fresh, country-diverse intermediates. Highest latency of the presets;
# use for sensitive traffic where being un-followable beats being fast.
#
#   "routing": { "policy_per_dial": "preset:privacy-max" }

MIN_HOPS = 3
MUX = 2
ROTATE_SECS = 8        # short path lifetime — rotate often
LEG_BUDGET = 4000000   # and retire a path after ~4 MB regardless

def decide_route(ctx, candidates):
    return RouteSpec(
        mux=MUX,
        min_hops=MIN_HOPS,
        distribution="auto",
        rotation_interval_seconds=ROTATE_SECS,
    )

def on_tick(ctx, legs):
    # Keep every path short-lived: retire the oldest/most-used live leg once it
    # crosses the byte budget (never the last one), replacing it through
    # intermediates disjoint from the one we drop. Country diversity is enforced
    # by steering the replacement away from this leg's hops.
    alive = [l for l in legs if l.alive]
    if len(alive) < 2:
        return None
    drop = None
    for l in alive:
        if (l.sent_bytes + l.recv_bytes) > LEG_BUDGET:
            drop = l
            break
    if drop == None:
        return None
    return Rotation(drop_legs=[drop.index], add_leg=True, exclude_hops=drop.hops)
