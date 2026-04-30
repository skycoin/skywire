# CXO_VS_DHT.md

A retrospective on Skywire's choice between two decentralized state primitives that are both already implemented in this repo: the Kademlia DHT (`pkg/dht/`) and CXO TreeStore feeds (`pkg/cxo/`). Could the work the DHT does today have been done with CXO? Mostly yes. Should it have been? Probably yes. Does that mean we should rip out the DHT? No — it works, it's spec'd, and migration cost dwarfs the win. But the framing of "DHT is the future" needs adjustment, and *new* decentralized state should default to CXO.

This document is descriptive, not normative. The DHT has its own spec at [skywire-specs/specifications/26-DHT.md](./skywire-specs/specifications/26-DHT.md) and a comparison against other Kademlia implementations at [SKYWIRE_DHT.md](./SKYWIRE_DHT.md). Read this one for the "could we have done this with the other primitive?" question.

## What each primitive provides today

### DHT (`pkg/dht/`)

- BEP44-style mutable signed records keyed by `SHA256(pubkey || salt)`
- Kademlia routing table with K=20, alpha=3, 256-bit ID space
- Iterative `find_node` / `get_value` lookups
- "Full node" mode that disables XOR-distance gating so a node stores everything received
- Active full-node reconciliation (paginated pull + batched push, every hour)
- Hybrid clients that read from DHT first and fall back to HTTP discovery
- Per-salt namespaces: `dmsg`, `tp`, `tp:1`/`tp:2`/… (chunked), `svc`, `addr`, `fullnode`, `dmsg-server`

Wire transport: DMSG streams on port 100 (`skyenv.DmsgDHTPort`).

### CXO TreeStore (`pkg/cxo/treestore/`)

- Content-addressable Merkle DAG of objects (`TreeNode` → `TreeEntry` → `Sub` or inline `Leaf`)
- Per-publisher feeds keyed by the publisher's PK (no separate feed keypair)
- Hierarchical path semantics: `tiers/dmsg/2026-04-27`, `visor/03ab…/transports/<id>`
- Append-only history with full audit trail; subscribers reuse unchanged-hash branches and fetch only the diff
- Subscribers connect by feed PK and walk the tree via the `Subscriber` API
- Already used in production for visor self-tracking telemetry aggregated by TPD (`pkg/transport-discovery/cxoaggregator/`)

Wire transport: also CXO over DMSG.

## The discovery use case mapped to both

The four discovery services the DHT is replacing — `dmsg-discovery`, `transport-discovery`, `service-discovery`, `address-resolver` — all want the same thing: "let visors publish their current state under their PK; let any other visor read it." Both DHT and CXO TreeStore can do this:

| Need | DHT today | CXO TreeStore equivalent |
|---|---|---|
| One value per (visor, kind) | Salt-namespaced PK target: `SHA256(pk \|\| "dmsg")`, `SHA256(pk \|\| "tp")`, … | Per-publisher feed root with typed entries at paths like `dmsg`, `transports/<id>`, `services/<type>` |
| Signed by publisher | BEP44 `K`+`Sig`+`Seq` over a length-prefixed payload | CXO feeds are signed natively at the feed-Root level |
| Monotonic updates | `seq > stored.seq` rejection | Feed appends only; head moves forward |
| Network-wide replication | Full-node reconcile pulls everything across known full nodes | Subscribe to every publisher → same effective topology |
| Bandwidth-efficient transport | DMSG streams, 4 MiB cap per RPC | DMSG streams; only the changed Merkle subtree branches transfer |
| HTTP fallback | Hybrid clients (`HybridDiscClient`, `HybridTPDClient`) | Could trivially layer the same |
| Per-publisher cardinality limits | Public-tier rate limit (50 items per PK) and `MaxValueSize` 64 KiB cap | Per-feed inherent (one feed per publisher); content-addressed dedup means unchanged objects don't cost more bytes |

The mapping is one-to-one for everything except discovery primitives.

## Where the DHT genuinely differs

**Lookup-by-key without prior subscription.** Iteratively walking K-buckets to find an arbitrary PK's data is the primitive that distinguishes a DHT from a federated database. With CXO you have to subscribe to a publisher first, which means you need a way to *find* publishers — a directory or a flood-discovery layer.

But this primitive isn't actually load-bearing in production Skywire. Every visor either:

1. Runs a full node (or talks to a hypervisor that runs one), so the lookup hits the local store immediately and never iterates, or
2. Falls through to HTTP discovery, which is the centralized service we're supposedly migrating away from.

The K-bucket routing table maintenance and iterative lookup machinery are **overhead we rarely use**. The full-node-reconcile loop we added in this branch made the DHT's actual replication topology identical to "every full node subscribes to every publisher" — which is exactly the CXO model with extra steps. We pay the cost of Kademlia routing without paying off the value of Kademlia routing.

**Claim:** if the network ever grows to a state where most visors are *not* full nodes and need to look up arbitrary peers' data they don't pre-subscribe to, the DHT would start earning its keep. We are not in that state today and the bandwidth cost of full-node mode at current scale doesn't motivate moving toward it.

## Where CXO is arguably the better fit

- **History is native.** "What did this visor's transport list look like 3 days ago?" is a feed-walk in CXO. In the DHT it's gone — the previous value was superseded by the latest seq and there's no mechanism to recover it. Several future use cases (route quality history, peer reputation, bandwidth-rewards audit) want this.

- **Deduplication of unchanged records.** CXO content-addresses every object, so an unchanged transport list is the same Merkle hash. The DHT republishes the entire payload every cycle even when nothing changed, then full-node reconcile re-pushes it across the network. Hub edges with hundreds of transports do this every 60 seconds.

- **Already-built infrastructure.** `pkg/cxo/`, the TreeStore aggregator (`pkg/transport-discovery/cxoaggregator/`), the user-feeds wiring, CXO-over-DMSG transport, the per-visor self-tracking telemetry pattern — all exist. The DHT we built from scratch.

- **Conceptual fit with the rest of skywire.** Visors already publish telemetry to CXO feeds. Adding `dmsg`/`tp`/`svc` to that pile is consistent. The DHT introduces a parallel mechanism with its own semantics, its own peer set, its own port, its own storage backend, its own metric counters.

- **No data-format mess.** The DHT's `tp` salt has four shapes coexisting on the wire (bare `[]Entry`, signed `[]SignedEntry`, compact-array `[{r,t,l}]`, compact-envelope `{s, ts}`) because publishers evolved independently. CXO's schema is part of the registry; format evolution happens through schema versions, not silent shape coexistence.

- **Hub-edge size cap.** The DHT has a single 64 KiB `MaxValueSize` per item; we had to invent salt chunking (`tp:1`, `tp:2`, …) to publish hub edges with 200+ transports. CXO's Merkle DAG has no equivalent cap — each tree level is its own object, so a publisher with 1000 transports just publishes 1000 leaf entries naturally.

## Where the DHT is the better fit

- **Cross-publisher lookup with no prior knowledge.** A new visor joining the network can iteratively find any other visor's data without having to be told who's out there. The CXO equivalent needs a directory feed that everyone subscribes to.

- **BEP44 is well-specified externally.** Anyone implementing a Skywire client in another language can follow the BitTorrent spec. CXO's wire protocol is more of an internal Skycoin primitive.

- **Sunk cost.** We have the spec, the implementation, the operator tools (`dht status`, `dht peers`, `dht reconcile`, `dht list`), and a substantial amount of in-tree code that's now load-bearing. Reverting all of that for a marginal architectural improvement doesn't pay off.

- **Bootstrapping mental model.** "I run a full node and it auto-pulls everything" is a simpler mental model for new operators than "I subscribe to each of these feeds, here's how to manage the subscription set."

## What we actually ended up building

In the course of the DHT's evolution on this PR branch and the prior one, we kept adding mechanisms that bring it closer to CXO's model, not further from it:

- **Full-node reconciliation** (pull + push back, hourly). This is "subscribe to every other full node and replicate."
- **Advertised full-node discovery** via signed `FullNodeAdvert` in the `fullnode` salt. This is "publish a feed of who's a publisher."
- **Hub-edge chunking** across `tp:1`, `tp:2`, … This is a flat-namespace workaround for not having Merkle DAG natively.
- **Multi-shape tolerance** in `decodeTpItem` and `LookupAll` because publishers evolved independently. This is "we needed a schema registry."
- **DHT-to-HTTP mirror** (`pkg/dht/mirror_to_disc.go`) bridging DHT writes back to centralized services. This is acknowledging that BEP44 mutable items aren't enough for discovery's HTTP-API needs.

Every one of these is a feature CXO has by default.

## Forward path

We're not ripping out the DHT. The pragmatic split:

1. **Existing DHT salts stay where they are.** `dmsg`, `tp`, `svc`, `addr`, `fullnode`, `dmsg-server`. They work, they're spec'd, and migrating them now is churn with little payoff.

2. **New decentralized state goes to CXO.** Anything we'd add tomorrow:
   - Route quality history (latency / loss / throughput time-series)
   - Peer reputation and trust signals
   - Bandwidth-rewards rolldown audit trail
   - Per-visor configuration overrides distributed across operators
   - Dynamic policy / blocklist / allowlist updates

   For all of these, CXO's append-only-with-history model is a better fit than a flat mutable-state DHT salt.

3. **The framing in `26-DHT.md` and `SKYWIRE_DHT.md` softens.** Today both documents say "the DHT will eventually replace the centralized discovery services." That's narrowly true and a useful intermediate state. But the broader narrative of "the DHT is Skywire's decentralized-state primitive" should be retired. CXO is. The DHT is one specific subsystem that happens to do discovery.

4. **The DHT's full-node mode and reconcile loop become explicit, bounded, and *small*.** Once we accept that the DHT doesn't need to be the foundation for everything decentralized, we don't need to keep extending it past its sweet spot. Anything that wants append-only history, complex nested state, or schema-evolution semantics gets pushed to CXO instead of getting bolted onto the DHT as another salt with its own format conventions.

## TL;DR

The DHT is a working solution to a narrower problem than its current spec framing implies. CXO TreeStore is the broader decentralized-state primitive that's already in the tree, already used for telemetry aggregation, and already has every property the DHT had to grow into. New work should default to CXO; the DHT keeps doing discovery because it already does discovery, but it shouldn't be the answer to "where does this new piece of distributed state live?"

## See also

- [skywire-specs/specifications/26-DHT.md](./skywire-specs/specifications/26-DHT.md) — DHT normative spec
- [SKYWIRE_DHT.md](./SKYWIRE_DHT.md) — DHT vs other Kademlia implementations
- `pkg/cxo/treestore/` — TreeStore implementation
- `pkg/transport-discovery/cxoaggregator/` — production example of CXO subscription + aggregation pattern
- `pkg/visor/cxo_user_feeds.go` — per-visor CXO feed registry on every visor
