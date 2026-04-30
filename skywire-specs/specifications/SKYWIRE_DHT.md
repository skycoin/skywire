# SKYWIRE_DHT.md

A comparison of Skywire's DHT (`pkg/dht/`) with prior-art Kademlia and DHT-adjacent designs, plus a discussion of where Skywire diverges, why, and what tradeoffs result.

The companion document [26-DHT.md](./26-DHT.md) is the normative specification. This document is descriptive: it explains design choices in context.

## Capsule summary

| Property | Skywire DHT |
|---|---|
| Lineage | Kademlia (BEP44 mutable items, secp256k1 instead of ed25519) |
| Bucket size K | 20 |
| Lookup parallelism α | 3 |
| Identity | Visor's secp256k1 pubkey → `SHA256(pubkey)` for the 256-bit node ID |
| Transport | DMSG streams (port 100), TCP-framed; falls back to skywire transports via `extraTransports` |
| Wire encoding | JSON (length-prefixed, 5-byte header) |
| Item value cap | 64 KiB |
| RPC max message | 4 MiB |
| Storage tiers | Whitelisted (no eviction, no TTL), Trusted (same), Public (LRU + 2 h TTL + 50/PK rate limit) |
| Full-node mode | Yes — opt-in storage of every item regardless of XOR distance |
| Active reconciliation | Hourly bidirectional pull + push between full nodes |
| Persistence | Memory, bbolt, or Redis |
| Discovery role | Mirrors and (eventually) replaces HTTP DMSG-discovery, transport-discovery, service-discovery, address-resolver |

## Lineage and inspirations

Kademlia (Maymounkov & Mazières, 2002) is the trunk. The two specs Skywire borrows the most from:

- **BitTorrent BEP5** (Kademlia DHT for peer exchange) — XOR routing, K-buckets, iterative `find_node` / `find_value`, refresh on activity. Skywire copies all of this with minor parameter tweaks.
- **BitTorrent BEP44** (mutable / immutable items) — signed value items keyed by `SHA1(pk + salt)`, monotonic `seq`, owner-only writes via signature check, optional CAS. Skywire copies the *semantics* but swaps the crypto to match the rest of the network.

Other comparable systems referenced below: **libp2p Kademlia** (IPFS), **Ethereum Discovery v5 (discv5)**, and **Tor onion service descriptors**, the latter being an interesting non-Kademlia design point.

## Side-by-side comparison

### Identity

| | Skywire | BitTorrent BEP5 | libp2p Kademlia | Ethereum discv5 | Tor onion v3 |
|---|---|---|---|---|---|
| Hash | SHA-256 | SHA-1 | SHA-256 (over multihash) | Keccak-256 | SHA3-256 |
| ID space | 256 bits | 160 bits | 256 bits | 256 bits | 256 bits |
| Signature scheme | secp256k1 (compressed 33 B pubkey, 65 B sig) | None on node ID; BEP44 uses ed25519 | varies (RSA, secp256k1, Ed25519, ECDSA) | secp256k1 | Ed25519 |

**Why secp256k1?** Every Skywire visor *already* has a secp256k1 keypair (used for noise sessions, transport entries, app signatures). Re-using that key as the DHT identity means there's nothing new to provision and the DHT identity ties to the visor identity automatically. BitTorrent BEP44 chose ed25519 in 2014 because it was faster and library-supported; Skywire pays a small CPU tax (`secp256k1` verify is 5–10× slower than ed25519) in exchange for one keystore instead of two.

### Routing table

| | Skywire | BEP5 | libp2p | discv5 |
|---|---|---|---|---|
| K (bucket size) | 20 | 8 | 20 | 16 |
| Number of buckets | 256 | 160 | 256 | 256 |
| Stale eviction | LRU eviction with ping | LRU + ping | LRU + ping (configurable) | Liveness checked on use |
| α (lookup parallelism) | 3 | 3 | 3 | 3 |

K = 20 is the original Kademlia paper's default and matches libp2p. BEP5's K = 8 is a BitTorrent-specific choice for the smaller 160-bit space. Nothing surprising here.

### Item model

| | Skywire | BEP44 (BitTorrent) | libp2p (IPNS records) | Tor onion descriptors |
|---|---|---|---|---|
| Mutable | Yes | Yes (mutable variant) | Yes (versioned) | Yes (revision number) |
| Owner-keyed | Yes (signer == publisher PK) | Yes | Yes | Yes |
| Multi-publisher per key | No (`SHA256(pk \|\| salt)` derives target) | No | No | No |
| Salt namespace | Yes (`pkg/dht/item.go`) | Yes (BEP44 §`salt` field) | Implicit (record type prefix) | N/A |
| Signature canonical form | `seq \|\| len(V) \|\| V \|\| len(salt) \|\| salt` | Bencoded `3:seq...1:v...4:salt...` | Protobuf-encoded fields | TLV with explicit lengths |
| Value cap | 64 KiB | 1 000 B | ~10 KiB recommended | ~50 KiB |
| Sequence semantics | Monotonic (`seq > stored.seq`) | Monotonic | Monotonic | Monotonic |

**Why 64 KiB and not 1 KB?** BEP44's 1 000-byte cap exists because BitTorrent DHT runs over UDP and that's the largest fragment-free packet on most networks. Skywire DHT runs over **DMSG streams** — TCP-framed — so there is no fragmentation concern. The cap was raised from the historical 16 KiB to 64 KiB after observing hub-edge transport lists silently truncating: a visor with 800+ transports needs ~80 bytes per compact entry, exceeding 16 KiB at ~200 entries. The cost is bandwidth on each Put and storage per item, both bounded.

**Why a length-prefixed signature payload?** The vulnerability without it: if the canonical form is `seq || V || salt`, then a value `V'` and salt `salt'` exist such that `V' || salt' = V || salt` for different `V`/`salt` splits — the signature would verify on the wrong split. Length-prefixing each field removes that ambiguity. BEP44 uses bencoded field names (`3:seq...1:v...`), which is also unambiguous but more verbose. Length-prefixed binary is what discv5 uses too.

### Storage tiers

This is Skywire's biggest divergence from Kademlia tradition.

Kademlia, as published, has **one storage class**: each node stores items whose target is among the K closest to its ID, evicting on TTL. BEP44 keeps that. libp2p and discv5 keep that.

Skywire adds:

| Tier | Eviction | TTL | Rate limit | Use case |
|---|---|---|---|---|
| Whitelisted | Never | Never | Bypassed | Hardcoded deployment-critical PKs (e.g., the transport setup nodes' identities) |
| Trusted | Never | Never | Bypassed | Known-good operator PKs |
| Public | LRU when pool > 5 000 | 2 h | 50 items per PK | Everyone else |

**Why?** The published Kademlia trust model is "all peers equal" — fine for an open network where anyone can publish anything. Skywire is trying to bootstrap a network where deployment services need *guaranteed* presence (their entries must never be evicted by churn) while remaining open to arbitrary visor publishes (which need rate limits to prevent flooding). The three tiers carve out those distinct cases. The trust policy is dynamic: changing it re-classifies stored items immediately.

The closest analog is **discv5's "ENRs" with topic advertising**, where high-prestige topics get dedicated routing-table slots, but the mechanism is quite different.

### Transport and encoding

| | Skywire | BEP5/BEP44 | libp2p | discv5 |
|---|---|---|---|---|
| Transport | DMSG (TCP-over-relay), optional skywire transports | UDP (with retransmits) | TCP/QUIC streams | UDP |
| Wire encoding | JSON | Bencode | Protobuf | RLP |
| Per-RPC timeout | 10 s | ~5 s | ~10 s (libp2p Stream timeout) | 0.5 s |
| Max message | 4 MiB | ~1300 B (UDP MTU) | 64 MiB | ~1280 B (UDP MTU) |

**Why DMSG and not direct UDP?** Skywire visors are mostly behind NATs without UPnP. DMSG is the network's relay-of-last-resort: every visor has a session to several DMSG servers, every other visor can be reached via those servers. Running the DHT on top of DMSG inherits that reachability for free. The cost is a hop through the DMSG server, but DHT operations are not latency-critical.

**Why JSON and not a binary encoding?** Operational debuggability. JSON is easy to dump, diff, and pretty-print from CLI tools; protobuf/bencode require a schema-aware decoder. The wire is bigger but DHT message volume is small. If we ever wanted, switching the encoding is local to `pkg/dht/rpc.go` (writeMsg/readMsg).

### Eclipse / Sybil resistance

Kademlia is famously vulnerable to two attacks:

1. **Eclipse**: an attacker fills the K-closest peers around a target, controlling all reads/writes to that target.
2. **Sybil**: an attacker spawns many cheap identities to skew routing-table content network-wide.

Mitigations across implementations:

| | Skywire | BEP5 | libp2p | discv5 |
|---|---|---|---|---|
| ID = `H(PK)` (binds ID to crypto identity) | ✓ | ✗ (random ID) | ✓ | ✓ |
| Ping verification on bucket update | ✓ | ✓ | ✓ | ✓ |
| Signed items (only owner can update) | ✓ (BEP44-style) | mutable items only | ✓ (IPNS) | ✗ (records, not values) |
| Static peer ID over IP changes | ✓ (PK is independent of address) | ✓ | ✓ | ✓ (ENR sequence) |
| Multi-publisher reads (find K, take highest seq) | ✓ | ✓ | ✓ | N/A |
| Trust tiers (whitelisted/trusted overrides) | ✓ — only Skywire | ✗ | ✗ | ✗ |

Skywire's `ID = SHA256(PK)` binding means an attacker can't pick which buckets they land in cheaply: forging a near-target ID means brute-forcing keys until `SHA256(pk)` is close enough, which is bounded by hash difficulty. BEP5's random IDs make this attack trivial; that's why BEP5 swarms saw real eclipses in 2017.

The **trust tier** mechanism is Skywire's belt-and-suspenders: even if an eclipse succeeds for a public-tier item, whitelisted/trusted entries remain available because they bypass the public pool's eviction.

### Bootstrapping and reconciliation

This is where Skywire diverges most heavily from Kademlia tradition.

**Standard Kademlia bootstrap:**
1. Ping a few well-known seed nodes
2. Iterative `find_node(self_id)` to populate buckets
3. Drift into steady-state, accumulating values via passive `store` requests for keys near our ID

That's what BEP5 and libp2p do. Skywire does this too — but only for the routing table.

**Skywire full-node bootstrap (added):**
4. Iterate the merged set of `BootstrapPKs` (deployment full nodes) ∪ peers with a fresh `FullNodeAdvert` (other visor full nodes)
5. Per peer: `Reconcile = pull (paginated GetItems) + push (PutBatch of items the peer doesn't have)`
6. Re-run hourly

Why standard Kademlia isn't enough for Skywire:

- BEP44 was designed for sparse mutable data (one record per torrent, a few thousand torrents). Skywire DHT mirrors the full transport-discovery dataset (3 800+ entries today, growing) and full DMSG-discovery dataset (1 400+). Passive accumulation through Puts hitting the K-closest converges far too slowly for this.
- Eventually-consistent passive replication breaks for "partial" full nodes: a node that has 30% of items and a peer that has 30% of items will both stay at 30% indefinitely if they only ever write near their own IDs. Skywire's reconcile is the only way the union materialises.

The **`FullNodeAdvert` salt** (`"fullnode"`) is novel: it's a self-attestation that says "I accept the responsibility of storing arbitrary mirror puts." Pushing to a peer without this advert is unsafe (the receiver-side `PutMirror` is unconditional). Discovery via the advert lets the network grow new full nodes without redeploying every existing one's `BootstrapPKs` config.

The closest comparable design point is **OrbitDB's "anchor peers"** in IPFS — designated replicators that pin the full dataset — but those require manual pinning and don't auto-discover each other.

### Discovery service replacement

A unique feature: Skywire's DHT is not just a key-value store, it's a *strategy* for replacing four centralized HTTP services (DMSG-discovery, TPD, SD, address-resolver). Each maps to a salt:

| Salt | Subject (target derivation) | Value | Replaces |
|---|---|---|---|
| `dmsg` | Visor PK | `disc.Entry` (DMSG addr, delegated servers) | http://dmsgd.skywire.skycoin.com |
| `tp` | Visor PK | List of compact transport entries from this visor | http://tpd.skywire.skycoin.com |
| `svc` | Visor PK | List of `servicedisc.Service` for this visor | http://sd.skycoin.com |
| `addr` | Visor PK | Address resolver record | http://ar.skywire.skycoin.com |
| `fullnode` | Full-node PK | Self-attestation (see above) | — (new) |
| `dmsg-server` | DMSG server PK | Server entry (addr, max sessions, peers) | (auxiliary) |

Each of these has a write-side (the `*Adapter` types) and a read-side. The `*Hybrid*Client` wrappers (`pkg/dht/disc_hybrid.go`) provide DHT-first reads with HTTP fallback, so the DHT can have partial coverage during rollout without breaking anything. This pattern is very much a Skywire-specific design — it's the "incremental decentralization" plan, where the centralized service runs as a DHT mirror itself, and as more visors publish to DHT directly, the central one becomes redundant.

No comparable system has this exact pattern. The closest parallels:

- **IPFS** has IPNS for mutable records, but it's not a hybrid migration off centralized DNS — it's a parallel namespace.
- **Mainline DHT** for BitTorrent supplements trackers without trying to replace tracker URLs.
- **Tor's HSDir** *did* replace the v2 onion-service "introduction point" centralized lookup with v3's distributed `HSDir` ring, which is the closest architectural analog. v3 took ~5 years to fully roll out; Skywire is roughly mid-way through the equivalent transition.

### What Skywire's DHT does *not* do (vs. peers)

- **No CAS / compare-and-swap**: BEP44 lets writers attach a `cas` of the previous `seq` to detect concurrent updates. Skywire's monotonic-seq check is weaker — two publishes with seq+1 race, last write wins. Adding CAS would be ~10 lines.
- **No iterative-write fallback**: when a Put can't reach K-closest, Skywire stores locally and logs a warning. BEP44 retries on a slowly-widening peer set. Skywire's full-node reconcile makes this less critical — the next reconcile pass will propagate.
- **No "republish on receive"** (Kademlia's classic durability mechanism): when a node receives a value, it does not push it onward. Re-publish is owner-driven only. Skywire trades republish-amplification for the more deterministic full-node reconcile.
- **No DHT-level rate limiting per IP**: only per-PK, since the DHT operates above noise-encrypted DMSG streams where IP isn't directly visible. This is in line with the Skywire philosophy of "PK is the identity, IP is incidental."
- **No XOR-distance based item routing for Puts in full-node mode**: a full node accepts any Put-target combination, not just close ones. This is intentional (full nodes are sinks by design) but means an attacker with one full-node-trusted relationship can spam an unbounded number of items there. Public-tier rate limit (50 items per PK) is the only defence against that today.

## Implementation footprint

For reference, comparing roughly the lines of code in each codebase's DHT implementation:

| Project | DHT LoC (approx) |
|---|---|
| BitTorrent libtorrent DHT (`src/kademlia/`) | ~10 000 (C++) |
| go-libp2p-kad-dht | ~12 000 (Go) |
| ethereum/go-ethereum/p2p/discover | ~6 500 (Go) |
| Skywire `pkg/dht/` | ~4 000 (Go, including adapters and bbolt/redis backends) |

Skywire's implementation is small because it intentionally does not try to handle hostile peers in an open network — it runs over DMSG, where a noise-authenticated session is required to even reach the DHT port, and the trust tier mechanism handles operator-level abuse rather than wire-level adversaries.

## Where the implementation is still rough

This is an honest list, not a roadmap.

1. **Full-node-to-full-node reconciliation is hourly and unidirectional per pass**. After the user-side puts an item that lands on full node A, full node B sees it the next hour at the earliest. Should be sub-minute for "live" data.
2. **The receiver-side `PutMirror` admission is unconditional**. Pushed to a non-full-node, the receiver overflows its store. Fixed today by gating senders to bootstrap+advertised peers; if the gate ever leaks, no second line of defence.
3. **CLI exposes the store but not the routing table**. There's no `skywire cli visor dht peers` to dump the K-buckets — debugging routing problems requires log-grepping.
4. **The hybrid clients log "DHT miss → HTTP" at debug**. Operators don't have a hit-rate metric to know if the DHT is paying for itself.
5. **No DHT-side metrics export** at all. Should plug into the visor's existing Prometheus surface.
6. **`MaxValueSize = 64 KiB` is a single global cap**. A future need for larger payloads (e.g., transport history) requires either chunking or a new design.
7. **The `dht.svc` salt has had multiple data-format mistakes** (single object vs. list of services). The autoconnect path handles both for compatibility, but every other reader needs to too.

## See also

- [26-DHT.md](./26-DHT.md) — normative specification
- [21-Service_Discovery.md](./21-Service_Discovery.md), [04-Transport_Discovery.md](./04-Transport_Discovery.md), [05-Messaging_System.md](./05-Messaging_System.md), [20-Address_Resolver.md](./20-Address_Resolver.md) — the centralized services the DHT is replacing
- BEP44 — http://www.bittorrent.org/beps/bep_0044.html
- Kademlia paper — https://pdos.csail.mit.edu/~petar/papers/maymounkov-kademlia-lncs.pdf
- discv5 spec — https://github.com/ethereum/devp2p/blob/master/discv5/discv5.md
- Tor v3 onion service spec — https://gitweb.torproject.org/torspec.git/tree/rend-spec-v3.txt
