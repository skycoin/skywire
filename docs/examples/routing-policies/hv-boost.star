# hv-boost.star — peering directly with one of our hypervisors?
# open extra mux legs for resilience. Other peers get normal
# single-route behavior.
#
# `peers.is_hypervisor` checks the visor's configured Hypervisors
# list (the operator's control-plane keys). This is a per-peer
# check on the dial destination, not on transit hops.

def decide_route(ctx, candidates):
    if peers.is_hypervisor(ctx.peer_pk):
        return RouteSpec(mux=4, min_hops=1)
    return RouteSpec()
