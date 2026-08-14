# hypervisor-priority — fast, resilient paths for the low-bandwidth control
# channel (visor⇄hypervisor RPC / signalling); modest privacy for everyone else.
#
# This shapes CONTROL-PLANE dials — a visor talking to its hypervisor or an
# operator-trusted peer — which are latency- and resilience-sensitive but tiny
# in bytes; unlinkability is moot there (the hypervisor already knows its
# visors). It is NOT a data-plane policy: proxy / VPN bulk traffic should use a
# throughput preset (spread-bw, asymmetric-fanout, balanced), which — like every
# preset here — shapes skynet ROUTES over real transports, never the shared dmsg
# relay. (dmsg is the relay/signalling substrate; routed app traffic rides
# stcpr/sudph/stcp. Candidates even carry `transport_kinds` so a policy can
# explicitly prefer direct-tcp over any dmsg-relayed hop.)
#
# Uses the trust signals the visor already maintains (conf.Hypervisors and the
# dmsgpty whitelist) via peers.is_hypervisor / peers.is_trusted.
#
#   hypervisor  → mux=4, min_hops=1  → shortest path (direct where available),
#                 fanned out for resilience so the control channel survives a
#                 single path dying.
#   trusted     → mux=2, min_hops=1  → low-latency, lightly redundant.
#   everyone    → mux=2, min_hops=2  → route through a real intermediate; a
#                 little unlinkability for ordinary app traffic.
#
#   "routing": { "policy_per_dial": "preset:hypervisor-priority" }

def decide_route(ctx, candidates):
    if peers.is_hypervisor(ctx.peer_pk):
        return RouteSpec(mux=4, min_hops=1, distribution="auto", rotation_interval_seconds=0)
    if peers.is_trusted(ctx.peer_pk):
        return RouteSpec(mux=2, min_hops=1, distribution="auto", rotation_interval_seconds=0)
    return RouteSpec(mux=2, min_hops=2, distribution="auto", rotation_interval_seconds=30)
