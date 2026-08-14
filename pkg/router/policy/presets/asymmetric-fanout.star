# asymmetric-fanout — one lean forward path, a fanned-out reverse path.
#
# For download-heavy apps (web browsing, media, VPN pull traffic): the REVERSE
# direction aggregates multiple parallel download paths while the FORWARD
# direction rides a single cheap, low-hop path carrying requests + ACKs. This is
# the asymmetric case the router already supports natively via forward_mux /
# reverse_mux — a symmetric MUX=4 wastes upstream fan-out on a workload whose
# bytes are almost all inbound.
#
#   forward  = 1 leg,  min_hops 1  → shortest path (uses the direct transport
#                                    when one exists); low latency for requests.
#   reverse  = 4 legs, min_hops 2  → real intermediates, fanned out for
#                                    throughput and to spread reward-eligible
#                                    relay bandwidth across the network.
#
# NOTE: this is a STATIC asymmetry. Flipping the fan-out to whichever direction
# currently carries more bandwidth is a natural extension — on_tick already sees
# per-leg sent_bytes/recv_bytes — but needs a Rotation verb to re-balance the
# per-direction mux, which does not exist yet. Until then this preset assumes the
# common download-heavy shape.
#
# Select without compiling a file:
#   "routing": { "policy_per_dial": "preset:asymmetric-fanout" }

FWD_MUX = 1
REV_MUX = 4
FWD_MIN_HOPS = 1       # forward: shortest path, the direct transport where available
REV_MIN_HOPS = 2       # reverse: real intermediates (fan-out + reward credit)
ROTATE_SECS = 15
LEG_BUDGET = 8000000   # retire a leg after ~8 MB, spreading over relays over time

RETX_PENALTY_MS = 50   # each SACK retransmit adds this much "badness" (loss >> latency)
BAD_LEG_FACTOR = 2     # shed a leg only when this many times worse than the best
MIN_BAD_MS = 200       # ...and only above this absolute badness (don't churn fast legs)

def decide_route(ctx, candidates):
    return RouteSpec(
        forward_mux=FWD_MUX,
        reverse_mux=REV_MUX,
        forward_min_hops=FWD_MIN_HOPS,
        reverse_min_hops=REV_MIN_HOPS,
        distribution="auto",
        rotation_interval_seconds=ROTATE_SECS,
    )

def _badness(l):
    # Lossy legs cost far more than merely slow ones.
    return l.latency_ms + l.retransmits * RETX_PENALTY_MS

def on_tick(ctx, legs):
    # Maintain the reverse fan-out the same way spread-bw does: shed the single
    # worst (lossy/slow) leg, else one over its byte budget, never the last live
    # leg — so downloads keep flowing while paths refresh onto fresh relays.
    alive = [l for l in legs if l.alive]
    if len(alive) < 2:
        return None
    worst = alive[0]
    best = alive[0]
    for l in alive:
        if _badness(l) > _badness(worst):
            worst = l
        if _badness(l) < _badness(best):
            best = l
    drop = None
    if _badness(worst) > MIN_BAD_MS and _badness(worst) > _badness(best) * BAD_LEG_FACTOR:
        drop = worst
    else:
        for l in alive:
            if (l.sent_bytes + l.recv_bytes) > LEG_BUDGET:
                drop = l
                break
    if drop == None:
        return None
    return Rotation(drop_legs=[drop.index], add_leg=True, exclude_hops=drop.hops)
