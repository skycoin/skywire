# spread-bw — benevolent bandwidth spreading (the "browse benevolently" preset).
#
# Routes app traffic (browser/Telegram/VPN over skysocks-client, etc.) across
# parallel MULTIHOP routes and continuously rotates each leg onto a fresh set
# of intermediates once it has carried a byte budget. Because min_hops >= 2
# forces every leg through real intermediate visors — and intermediates earn
# bandwidth-reward credit for what they relay — ordinary browsing through this
# policy distributes a reward-eligible bandwidth floor across many network
# visors instead of pinning to one path. Dynamic routing, not a static circuit.
#
# Note: the exit/proxy server itself is typically NOT reward-eligible, so the
# value here is the spread to the intermediate relays, not the exit.
#
# Select it without compiling or shipping a file:
#   "routing": { "policy_per_dial": "preset:spread-bw" }

MUX = 4               # parallel legs — spread across MUX intermediates at once
MIN_HOPS = 2          # >= 2 forces real intermediates (excludes the direct transport)
ROTATE_SECS = 20      # rotation cadence
LEG_BUDGET = 8000000  # rotate a leg after it has carried ~8 MB, spreading over time

def decide_route(ctx, candidates):
    return RouteSpec(
        mux=MUX,
        min_hops=MIN_HOPS,
        distribution="auto",
        rotation_interval_seconds=ROTATE_SECS,
    )

def on_tick(ctx, legs):
    # Retire any leg that has carried its byte budget, excluding its
    # intermediates from the replacement so the next leg lands on fresh
    # relays — this is what spreads the bandwidth around over time.
    drop = []
    excl = []
    for l in legs:
        if l.alive and (l.sent_bytes + l.recv_bytes) > LEG_BUDGET:
            drop.append(l.index)
            excl = excl + l.hops
    if len(drop) == 0:
        return None
    return Rotation(drop_legs=drop, add_leg=True, exclude_hops=excl)
