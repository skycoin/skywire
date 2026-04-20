# Kademlia DHT

The Distributed Hash Table (`pkg/dht/`) provides decentralized key-value storage for the Skywire network. It implements Kademlia routing with BEP44 mutable data semantics, adapted for secp256k1 cryptography.

## Purpose

The DHT supplements and will eventually replace centralized discovery services (DMSG discovery, transport discovery, service discovery, address resolver). Every visor with a DMSG client runs a DHT node automatically.

## Node Identity

DHT node IDs are 256-bit values derived from the visor's secp256k1 public key:

```
NodeID = SHA256(compressed_secp256k1_pubkey)
```

This deterministically maps each visor's existing identity to a position in the DHT address space. No separate key generation is required.

## Routing Table

The routing table consists of 256 k-buckets (one per bit of the 256-bit ID space), each holding up to k=20 peers. Peers are ordered by last-seen time within each bucket. The bucket index for a peer is determined by the common prefix length of `XOR(self_id, peer_id)`.

When a bucket is full and a new peer is discovered:
- If the least-recently-seen peer responds to a ping, the new peer is discarded (prefer long-lived nodes)
- If the least-recently-seen peer does not respond, it is evicted and the new peer is added

## Mutable Items

Items are signed, updateable records stored in the DHT. Each item has:

| Field | Type | Description |
|---|---|---|
| `K` | `cipher.PubKey` (33 bytes) | Publisher's secp256k1 public key |
| `Seq` | `uint64` | Monotonically increasing sequence number |
| `Sig` | `cipher.Sig` (65 bytes) | secp256k1 signature over the canonical payload |
| `V` | `[]byte` (max 16384 bytes) | Value |
| `Salt` | `[]byte` (max 200 bytes) | Optional namespace for key disambiguation |

### Target Key

Items are stored at DHT nodes closest (by XOR distance) to their target key:

```
Target = SHA256(K || Salt)
```

### Signature

The signature covers a canonical payload with length-prefixed fields to prevent concatenation ambiguity:

```
payload = seq (8 bytes BE) || len(V) (4 bytes BE) || V || len(Salt) (4 bytes BE) || Salt
signature = secp256k1_sign(SHA256(payload), secret_key)
```

### Monotonic Sequence

Storing nodes reject puts where `item.Seq <= stored.Seq`. Only the owner (holder of the secret key) can increment the sequence and publish updates.

### TTL and Expiry

- Public items expire after 2 hours without re-announcement
- Whitelisted and trusted items never expire via TTL
- Publishers re-announce every 60 seconds (visor's `dhtPublishLoop`)
- Items can also be refreshed by any node that holds a copy (re-push without the private key, using the existing signature)

## RPC Protocol

DHT nodes communicate via length-prefixed JSON messages over DMSG streams (port 100). Each message has a 5-byte header:

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

Per-RPC timeout: 10 seconds. Maximum message size: 64 KB.

The `mirror_target` field in PutValue allows deployment services to store items under a target key derived from a different PK (used for mirroring entries from visors that don't dual-write to the DHT).

## Iterative Lookup

Lookups follow standard Kademlia with alpha=3 concurrency:

1. Seed the shortlist with the K closest peers from the local routing table
2. In parallel (up to alpha=3), query the closest unqueried peers via FindNode or GetValue
3. Merge returned peers into the shortlist (verify `peer.ID == SHA256(peer.PK)` to prevent poisoning)
4. Sort by XOR distance, trim to K
5. Repeat until all K closest have been queried
6. For value lookups, track the highest-sequence item across all responses

## Trust Tiers

The store classifies items by publisher PK:

| Tier | Eviction | TTL Expiry | Rate Limit |
|---|---|---|---|
| **Whitelisted** | Never evicted | No TTL | Bypassed |
| **Trusted** | Never evicted | No TTL | Bypassed |
| **Public** | LRU eviction when pool exceeds capacity | 2 hours | Max 50 items per PK |

Trust classification is dynamic — changes to the trust policy take effect immediately on all stored items.

## Full Node Mode

A full node stores all items regardless of XOR distance to its own ID. Normal nodes only store items close to their ID (standard Kademlia behavior). Deployment services run as full nodes to ensure complete data availability for bootstrap.

Full node mode can be toggled at runtime: `skywire cli visor dht full-node on|off`.

## Bootstrap

On startup, the DHT node:

1. Pings each bootstrap peer to populate the routing table
2. Performs an iterative self-lookup (`FindNode(self_id)`) to discover nearby peers

Default bootstrap peers are the deployment service PKs (extracted from the visor's config). No additional configuration is needed.

## Discovery Adapters

The DHT provides adapter types that implement the same interfaces as the centralized discovery clients:

| Adapter | Interface | Salt | Description |
|---|---|---|---|
| `DiscAdapter` | `disc.APIClient` | `dmsg` | DMSG discovery entries |
| `TPDAdapter` | `transport.DiscoveryClient` | `tp` | Transport entries |
| `SvcAdapter` | — | `svc:<type>` | Service records |
| `AddrAdapter` | — | `addr` | Address resolver records |

`HybridDiscClient` and `HybridTPDClient` wrap a DHT adapter with an HTTP fallback: reads try DHT first, writes go to both.

## Entry Mirroring

Deployment services run DHT full nodes and mirror entries received via HTTP to the DHT. This ensures entries from old visors (that don't dual-write) are available in the DHT.

The mirror signs the DHT item with the service's own key but stores it under the visor's target key (`SHA256(visor_pk || salt)`) using `PutMirror` which allows the signer PK to differ from the target PK. The entry's own application-level signature (e.g., `disc.Entry.Signature`) provides authenticity proof.

## Configuration

```json
{
  "dht": {
    "full_node": false,
    "bootstrap_pks": [],
    "whitelisted_pks": [],
    "trusted_pks": []
  }
}
```

All fields are optional. DHT is enabled automatically when DMSG is available. If `bootstrap_pks` is empty, deployment service PKs are used.
