# hv-boost.star — peering directly with one of the visor's
# configured hypervisors? Opt into mux=4 + min_hops=2 for
# redundancy. Other peers get visor defaults.
#
# `peers.is_hypervisor` checks the visor's conf.Hypervisors list
# (the operator's control-plane keys). This is a per-peer check
# on the dial destination, not on transit hops.
#
# Two-phase invocation:
#   - BeforeDial (candidates empty): ctx-only — peer is the
#     hypervisor? set mux=4 + min_hops=2.
#   - SelectRoute (candidates populated): defer to the router's
#     disjoint-path pick (which will return up to 4 disjoint
#     multihop routes thanks to mux=4).
#
# NOTE: min_hops=2 forces multihop. If you'd rather hypervisor
# dials use the direct transport when one exists (and skip mux),
# drop min_hops and let the router's direct-transport short-
# circuit kick in.

def decide_route(ctx, candidates):
    if peers.is_hypervisor(ctx.peer_pk):
        return RouteSpec(mux=4, min_hops=2)
    return RouteSpec()
