# RFC: Bandwidth aggregation over the mesh — connection-striped independent flows

**Goal:** *Saturate the card, not the mesh.* The skysocks proxy should fill the
machine's true endpoint bottleneck (NIC / router / ISP) in both directions by
aggregating bandwidth across many diverse disjoint routes, with the mesh routing
never the limiting factor. At saturation, per-route data flow should be
**unidirectional** (a saturated leg commits to one direction) as conditions allow.

## Where we are

A single skysocks stream now reaches ~9.5 MB/s (after raising the yamux window
256 KB → 16 MB, #4211 — the bandwidth-delay-product fix). On a 100 Mbps card one
leg nearly maxes it. On a **gigabit** card one leg is ~8%, so the proxy must
**sum** routes to fill the pipe. Today it cannot — and the reason is structural,
not a tuning gap.

## Why the current mux cannot aggregate

The mux route group carries the route group's **single NOISE-encrypted byte
stream** (`router_serve.go` wraps the RouteGroup with `network.EncryptConn`).
The mux stripes that one ciphertext stream's packets across N legs and the
receiver re-orders them (`pkg/router/reorder.go`). Noise is a **stateful AEAD**:
delivering past a missing sequence permanently desyncs the cipher (every later
frame fails its MAC → a hard 0-byte wedge), so the reorder buffer **must** hold a
gap until it is filled and deliver strictly in order (`reorder.go:74-103`).

Consequence: **every leg feeds one strictly-ordered sequence, so a gap on the
slowest leg head-of-line-blocks all of them.** N legs cannot exceed the slowest
active leg's rate. Enlarging the reorder window (`reorderWindow = 2048`) only
defers the stall; it does not remove the HoL dependency. Measured live: an
18-leg mux was *slower* (104 KB/s) than a single direct leg (250 KB/s) before the
window fix, and after it both sit at ~one-leg throughput — the mux selects, it
does not aggregate.

## The redesign: connection-striped independent flows

Stripe at the **connection level, not the packet level.** Each disjoint route
becomes an **independent tunnel** — its own route group, its own noise session,
its own yamux — and browser connections are spread across tunnels. K connections
over K disjoint legs = K independent ordered flows, **zero cross-leg reorder**,
throughput that genuinely sums. A single connection still rides one tunnel (~9.5
MB/s), which is correct: a lone stream can't be split without reintroducing the
HoL problem, but real workloads (browsing, parallel/segmented downloads) are many
connections and aggregate cleanly.

```
                         ┌── tunnel 1 (noise+yamux) ── leg A (disjoint) ──┐
browser conns ── stripe ─┼── tunnel 2 (noise+yamux) ── leg B (disjoint) ──┼── exit
  (least-loaded)         └── tunnel K (noise+yamux) ── leg C (disjoint) ──┘
```

- **Client** (`pkg/skysocks/client.go`): hold K sessions instead of one; assign
  each accepted browser conn to the least-loaded tunnel (`session[i].Open()`).
- **Dial** (`cmd/apps/skysocks-client`): open K route groups to the exit over
  **disjoint** paths. Disjointness is the coordination problem — each tunnel must
  leave the source over a **different first-hop transport** (else they share a
  link and split it, not sum it). Reuse the mux's disjoint-intermediate machinery
  (`ExcludeTransportIDs` / `ExcludeIntermediatePKs`) at the tunnel granularity:
  dial tunnel *i* excluding the transports/intermediates tunnels `1..i-1` claimed.
- **Server** (`pkg/skysocks/server.go`): unchanged — the exit already accepts and
  serves multiple sessions; K tunnels are just K clients from its view.
- **The packet-level mux stays** as the *within-tunnel* resilience layer (warm
  standby, instant failover, primary-swap — all the #4200-#4210 work): each
  tunnel MAY be a small mux for liveness, but its aggregation contribution is one
  leg's worth. Diversity/aggregation comes from K tunnels, resilience from the
  mux inside each.

## Unidirectional at saturation

A saturated leg should carry data one way; the reverse direction should be only
its ACK/control trickle. Connection striping gives this for free at the tunnel
level — a download connection's tunnel is download-dominant. The refinement:
under sustained saturation, **commit a tunnel/leg to a direction** and route the
opposite direction's *data* (not just ACKs) onto other tunnels, so no leg carries
two saturating flows against each other. This extends the adaptive tick's
existing forward/reverse split (`adaptFwdActive`/`adaptRevActive`, "forward-lean
is a send-side decision") from per-mux to per-tunnel, driven by the per-leg
SentBytes/RecvBytes the tick already tracks.

## Autonomous convergence (reuse, don't rebuild)

The tunnel set self-optimizes with the machinery already built this session:
- **throughput-eviction / latency-eviction / primary-swap**: retire a tunnel
  whose leg is a low-throughput / high-latency / high-loss outlier, redial a
  fresh disjoint one — the "figure out the best routes and cycle them in" loop.
- **warm-standby pool + instant failover**: pre-vetted disjoint routes ready to
  promote into a new tunnel the moment load rises or a tunnel dies.
- **scale on demand**: add tunnels while aggregate throughput still rises with
  each (BDP/goodput measured per tunnel); stop when it plateaus (the endpoint
  bottleneck) or disjoint paths run out; shed tunnels when idle.

## Acceptance

1. 100 Mbps machine: download and upload both ~100 Mbps.
2. Gigabit machine (magnetosphere): aggregate disjoint tunnels to the ~500 Mbps
   service ceiling — proof of true summation, not selection.
3. Tunnel set self-converges to the best routes, sheds drags, spreads across
   disjoint paths.
4. Aggregation scales up under load, relaxes when idle, independently per
   direction; saturated legs run unidirectional.
5. The wall you hit is the NIC/router/ISP — never the mesh.

## Incremental plan

1. **This doc.** Lock the direction.
2. Client K-session scaffolding + least-loaded connection striping, K configurable
   (K=1 ≡ today, safe default). Measure aggregation with K disjoint tunnels dialed
   manually.
3. Disjoint-tunnel dial coordination (per-tunnel transport/intermediate exclusion).
4. On-demand scaling (grow/shrink K by measured goodput) + tunnel eviction reusing
   the adaptive signals.
5. Unidirectional-at-saturation direction commitment.
6. Gigabit validation on magnetosphere.
