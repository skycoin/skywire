# Kademlia DHT

The Distributed Hash Table (`pkg/dht/`) provides decentralized key-value storage for the Skywire network. It implements Kademlia routing with BEP44 mutable data semantics, adapted for secp256k1 cryptography and an optional "full node" storage model.

## Purpose

The DHT supplements and is intended to eventually replace the centralized discovery services (DMSG discovery, transport discovery, service discovery, address resolver). Every visor with a DMSG client runs a DHT node automatically; whether it stores all items network-wide or only items close to its own ID depends on the `full_node` config flag.

## Node Identity

DHT node IDs are 256-bit values derived from the visor's secp256k1 public key:

```
NodeID = SHA256(compressed_secp256k1_pubkey)
```

This deterministically maps each visor's existing identity to a position in the DHT address space. No separate key generation is required — the DHT node and the visor share an identity.

## Routing Table

The routing table consists of 256 k-buckets (one per bit of the 256-bit ID space), each holding up to **K = 20** peers. Peers are ordered by last-seen time within each bucket. The bucket index for a peer is determined by the common prefix length of `XOR(self_id, peer_id)`.

When a bucket is full and a new peer is discovered:
- If the least-recently-seen peer responds to a ping, the new peer is discarded (prefer long-lived nodes — Kademlia's standard "old peer wins" heuristic)
- If the least-recently-seen peer does not respond, it is evicted and the new peer is added

## Mutable Items

Items are signed, updateable records stored in the DHT. Each item has:

| Field | Type | Description |
|---|---|---|
| `K` | `cipher.PubKey` (33 bytes) | Publisher's secp256k1 public key |
| `Seq` | `uint64` | Monotonically increasing sequence number |
| `Sig` | `cipher.Sig` (65 bytes) | secp256k1 signature over the canonical payload |
| `V` | `[]byte` | Value (max **65 536 bytes** — see `pkg/dht/item.go`) |
| `Salt` | `[]byte` | Optional namespace (max 200 bytes) |

The 64 KB `V` cap was raised from the BEP44 default of 1 000 bytes to accommodate hub-edge transport lists: a visor with 800+ transports needs ~80 bytes per compact entry, which exceeded the original 16 KB cap silently. Skywire's DHT runs over DMSG streams (TCP-framed), so UDP fragmentation isn't a concern.

### Target Key

Items are stored at DHT nodes whose IDs are closest (by XOR distance) to their target key:

```
Target = SHA256(K || Salt)
```

### Signature

The signature covers a canonical payload with length-prefixed fields to prevent concatenation ambiguity:

```
payload   = seq (8 bytes BE) || len(V) (4 bytes BE) || V || len(Salt) (4 bytes BE) || Salt
signature = secp256k1_sign(SHA256(payload), secret_key)
```

### Monotonic Sequence

Storing nodes reject puts where `item.Seq <= stored.Seq` (`pkg/dht/store.go:Put`). Only the owner (holder of the secret key) can increment the sequence and publish updates. A monotonic clock (`time.Now().UnixNano()`) is the canonical seq generator — guarantees climbing past any cached previous publish even after restart.

### TTL and Expiry

| Tier | Eviction | TTL Expiry | Rate Limit |
|---|---|---|---|
| **Whitelisted** | Never evicted | No TTL | Bypassed |
| **Trusted** | Never evicted | No TTL | Bypassed |
| **Public** | LRU eviction when pool exceeds `PublicPoolSize` (default 5 000) | `ItemTTL` (default 2 h) | Max `RateLimitPerPK` items per PK (default 50) |

Trust classification is dynamic — changes to the trust policy take effect immediately on all stored items.

Publishers re-announce every 60 seconds (visor's `dhtPublishLoop`). Items can also be refreshed by any node that holds a copy (re-push without the private key, using the existing signature).

## RPC Protocol

DHT nodes communicate via length-prefixed JSON messages over DMSG streams (port **100**, `skyenv.DmsgDHTPort`). Each message has a 5-byte header:

```
| length (4 bytes BE) | method (1 byte) | JSON payload |
```

Methods:

| Method | Tag | Request | Response |
|---|---|---|---|
| Ping | 1 | `{sender_id, sender_pk}` | `{responder_id, responder_pk}` |
| FindNode | 2 | `{sender_id, sender_pk, target}` | `{closest: [Peer]}` |
| GetValue | 3 | `{sender_id, sender_pk, target}` | `{item?, closest: [Peer]}` |
| PutValue | 4 | `{sender_id, sender_pk, item, mirror_target?}` | `{stored, error?}` |
| GetItems | 5 | `{sender_id, sender_pk, salt?, since_seq?, limit?}` | `{items, targets, has_more}` |
| PutBatch | 6 | `{sender_id, sender_pk, items[], targets[]}` | `{stored[], errors?[]}` |

Per-RPC timeout: 10 seconds. Maximum message size: **4 MiB** (`readMsg` in `pkg/dht/rpc.go`).

### Mirror puts

The `mirror_target` field on `PutValue` (and the parallel `targets` field on `PutBatch`) lets a sender store an item under a target key derived from a different PK than the item's signer. Used by deployment services to mirror entries received via legacy HTTP discovery into the DHT — the item's own application-level signature provides authenticity, the DHT layer just distributes.

Receiver-side `PutMirror` handlers do **not** check XOR distance or full-node admission. Senders are therefore expected to push only to peers that have explicitly opted in to receiving mirror puts: bootstrap PKs (configured) and peers with a fresh `FullNodeAdvert` (signed self-attestation). See **Reconcile** below.

### GetItems pagination

`GetItems` returns up to `limit` items (server-side default 1 000) sorted by `Seq` ascending, with `target` as a stable tiebreaker. The response carries `has_more=true` when more items exist beyond the batch boundary.

Callers paginate with a `since_seq` cursor: each batch advances the cursor to the highest `Seq` seen so far. Batches do not split a `Seq` tie across boundaries — the server extends a batch past `limit` to swallow all items at the boundary `Seq`, so the cursor is always safe to advance to that value without skipping ties.

## Iterative Lookup

Lookups follow standard Kademlia with **alpha = 3** concurrency:

1. Seed the shortlist with the K closest peers from the local routing table
2. In parallel (up to `Alpha=3`), query the closest unqueried peers via `FindNode` (or `GetValue` for value lookups)
3. Merge returned peers into the shortlist (verify `peer.ID == SHA256(peer.PK)` to prevent ID poisoning)
4. Sort by XOR distance, trim to K
5. Repeat until all K closest have been queried
6. For value lookups, track the highest-`Seq` item across all responses

## Full Node Mode

A **full node** stores all items regardless of XOR distance to its own ID. **Normal nodes** only store items close to their ID (standard Kademlia behavior, capped at `MaxItems`).

Deployment DMSG servers run as full nodes by configuration so the network has reliable bulk-data sources. Visor full nodes are opt-in (`dht.full_node = true` in `skywire-config.json`).

Full node mode can be toggled at runtime: `skywire cli visor dht full-node on|off`.

### Full-node bootstrap and reconcile

A node that only accepts items via passive `PutValue` accumulates data slowly: most Puts only reach K-closest peers, so even a full node sees only a fraction of network-wide writes. To converge, full nodes run an active **reconcile loop** (`pkg/dht/dht.go:fullNodePullLoop`):

- **At startup** (after a 5-second bootstrap warmup), iterate the merged set of `BootstrapPKs` ∪ peer-advertised full nodes (see below)
- **Per peer**: `Reconcile = pull + push-back`
  - **Pull**: paginated `GetItems` with empty salt, mirroring received items into the local store via `PutMirror`. Records the set of received target keys.
  - **Push**: paginate the local store; for each item with a target key NOT in the peer's pulled set, batch-push via `PutBatch`. Cross-pollinates items the peer was missing.
- **Periodic re-run**: every 1 hour. Per-peer timeout 5 minutes.

Convergence guarantee: after one reconcile pass against every other full node, the local store holds the union; after the *peers* run their own pass, all full nodes hold the same union.

### Full-node advertisement

Full nodes publish a signed `FullNodeAdvert` to salt `"fullnode"` every 5 minutes (`pkg/dht/fullnode_advert.go:AdvertiseFullNode`):

```json
{
  "pk": "<pubkey hex>",
  "stored_items": 3562,
  "full_node": true,
  "ts": 1777551386053786094
}
```

`FindAdvertisedFullNodes` returns the set of advertised PKs with a fresh `ts` (≤30 minutes old, allowing 5 missed publishes). The reconcile loop unions these with `BootstrapPKs` so full-node coverage discovers itself rather than depending on a static config.

Pushing to an advertised peer is safe because the signed advert is the peer's explicit declaration that it accepts mirror puts. Random routing-table peers that haven't advertised are excluded.

## Bootstrap

On startup, the DHT node:

1. Pings each `BootstrapPKs` peer to populate the routing table (`bootstrapLoop`)
2. Performs an iterative self-lookup (`FindNode(self_id)`) to discover nearby peers
3. **Full nodes only:** after a 5-second warmup, kicks off the reconcile loop above

Default bootstrap peers are the deployment DMSG-server PKs (extracted from `dmsg.Prod.DmsgServers` via `deployment.Services.DHTBootstrapPKs()`). DMSG servers run DHT full nodes themselves on port 100, gated by the dmsg-server config flag `enable_dht`.

Bootstrap retries: every 30 seconds while no peers are in the routing table, slowing to every 5 minutes once at least one is.

## Discovery Adapters

The DHT provides adapter types that implement the same interfaces as the centralized discovery clients:

| Adapter | Interface | Salt | Description |
|---|---|---|---|
| `DiscAdapter` | `disc.APIClient` | `dmsg` | DMSG discovery entries (each visor publishes its `disc.Entry` under its own PK) |
| `TPDAdapter` | `transport.DiscoveryClient` | `tp` | Transport entries — compact format `[{r: remote_pk, t: type, l: latency_ms, b: cumulative_bw_bytes}]`. Live snapshot only; daily history lives in the visor's CXO feed. |
| `SvcAdapter` | — | `svc` | Service records (a list of `servicedisc.Service` under each visor PK) |
| `AddrAdapter` | — | `addr` | Address resolver records |

`HybridDiscClient` and `HybridTPDClient` (`pkg/dht/disc_hybrid.go`) wrap a DHT adapter with an HTTP fallback: reads try DHT first, fall back to HTTP on miss; writes go to both. Wired into the visor by `initDHT` (DMSG hybrid) and `initDHTTransport` (TPD hybrid). The visor's autoconnect path (`pkg/visor/autoconnect.go:fetchPubAddresses`) reads the `svc` salt directly.

## Entry Mirroring

Deployment services (DMSG discovery, TPD, SD) mirror HTTP-received entries into the DHT so entries from old visors that don't dual-write are still available to DHT readers. The mirror signs the DHT item with the *service's* own key but stores it under the visor's target key (`SHA256(visor_pk || salt)`) using the `mirror_target` field. The entry's own application-level signature (e.g., `disc.Entry.Signature`) provides authenticity proof; the DHT signature provides distribution authenticity.

## Persistence

The store can be backed by:
- **Memory only** — default for short-lived clients (CLI, tests)
- **bbolt** (`pkg/dht/backend_bolt.go`) — single-file embedded DB. Default for visors when `dht.persist_path` is set; visor auto-defaults to `<local_path>/dht.db`.
- **Redis** (`pkg/dht/backend_redis.go`) — for deployment services that need shared-state across replicas. Selected when `dht.redis_addr` is set.

Backend precedence: `RedisAddr` → `PersistPath` → in-memory.

## CLI

The visor exposes the DHT via `skywire cli visor dht`:

| Command | Purpose | Useful with |
|---|---|---|
| `dht status` | Node ID, peer count, store size, full-node flag, lookup hit/miss counts | All nodes |
| `dht get <pk> [salt]` | Iterative `GetValue` for a target | All nodes |
| `dht put <value> [salt] --seq N` | Publish under this visor's key | All nodes |
| `dht list [--salt s] [--with-target]` | Dump local store as JSON. `--with-target` emits `{target, value}` so listings can be diffed against HTTP discoveries | Mainly full nodes — non-full nodes only hold items near their own ID |
| `dht sync [<full-node-pk>] [--salt s]` | One-shot paginated pull from a remote full node into the local store. Pull-only, no push | All nodes — but most useful for full nodes that want to fast-forward without waiting for the periodic reconcile |
| `dht full-node <on\|off>` | Toggle storage mode at runtime | Persistent toggle is via the `full_node` config flag |
| `dht peers` | Dump every K-bucket peer (PK, NodeID, last-seen, bucket index) | All nodes — primary debugging tool for "is the DHT actually connected to anyone?" |
| `dht reconcile <full-node-pk> [--salt s]` | Manually run a one-shot pull+push reconcile against a specific peer. Restricted to peers in `BootstrapPKs ∪ FindAdvertisedFullNodes` (signed full-node attestation required) | Full nodes — debugging cross-peer divergence or forcing convergence after a config change |

`route calc` also gained a `--source tpd|dht|auto` flag (default `tpd`):

- `tpd`: fetch `/all-transports` from the deployment TPD (current behavior).
- `dht`: build the graph from the local DHT's `tp` salt entries via `DHTListWithTargets`. Three formats are supported: bare `[]transport.Entry` (visor-published), `[]transport.SignedEntry`, and the compact single-letter format `[{r,t,l}]` published by deployment-side mirrors. The compact format omits the source PK in the value, but it's recovered by cross-referencing the storage target hash against the `dmsg` salt's `static` field — every visor that publishes both salts has a known PK→target relationship via `target = SHA256(pk||salt)`. Synthetic transport IDs are generated for compact entries (deterministic from the `(srcPK, rPK, type)` tuple).
- `auto`: try DHT first; fall back to TPD if DHT yielded fewer than 10 entries.

## Configuration

```json
{
  "dht": {
    "full_node": false,
    "persist_path": "local/dht.db",
    "redis_addr": "",
    "bootstrap_pks": [],
    "whitelisted_pks": [],
    "trusted_pks": [],
    "max_items": 0,
    "item_ttl": 0,
    "refresh_interval": 0,
    "public_pool_size": 0,
    "rate_limit_per_pk": 0
  }
}
```

All fields are optional. DHT is enabled automatically when DMSG is available. If `bootstrap_pks` is empty, deployment DMSG-server PKs are used. Zero-valued limits fall back to the defaults shown in the **TTL and Expiry** table.
