# sticky-tcp-flows.star — pin each TCP flow (5-tuple) to a single
# mux leg for its lifetime. Useful for vpn-client / vpn-server
# where packet reordering across legs hurts TCP congestion
# control (out-of-order ACKs trigger spurious retransmits).
#
# Layer-2 descriptor: "sticky:5tuple" — selector hashes the IPv4
# 5-tuple (src/dst IP + src/dst port + protocol) and picks
# hash %% live-leg-count. Same flow always lands on the same leg.
# Non-IPv4 payloads fall back to a payload-prefix hash (still
# deterministic, just without flow semantics).
#
# Two-phase invocation:
#   - BeforeDial (candidates empty): set mux + the sticky
#     descriptor. SelectRoute doesn't propagate distribution,
#     so this must be BeforeDial.
#   - SelectRoute (candidates populated): pick the first
#     candidate; the disjoint-mux loop adds the rest.

def decide_route(ctx, candidates):
    if ctx.app not in ("vpn-client", "vpn-server"):
        return RouteSpec()
    if not candidates:
        return RouteSpec(mux=3, distribution="sticky:5tuple")
    return RouteSpec(chosen=candidates[0])
