# Skychat Pairing — CXO-backed end-to-end private messaging

The legacy skychat path opens a direct skynet/dmsg connection between
two visors and exchanges plaintext bytes. It works for the simple
case but has two structural limits: (1) message content is in the
clear over the wire, and (2) there's no offline delivery — if the
peer isn't reachable when you press send, the message never arrives.

The pairing layer fixes both by mounting each conversation on a
dedicated CXO TreeStore feed: every visor publishes its own outbox
to a per-pair DMSG port; the peer subscribes; messages are AEAD-
encrypted with a key derived from ECDH(my_sk, peer_pk). CXO handles
content-addressed replication, so an offline peer catches up
automatically when it comes back online.

This document covers the design, threat model, HTTP/SSE schema,
configuration, and operational notes. The implementation lives in
`cmd/apps/skychat/pairing/` (transport-agnostic primitives),
`pkg/visor/pairing.go` (visor RPC surface), and
`cmd/apps/skychat/commands/pairing.go` (HTTP layer + handshake).

## Architecture at a glance

```
Alice's visor                        Bob's visor
─────────────                        ───────────
 ┌──────────────┐                    ┌──────────────┐
 │ pairing.Mgr  │                    │ pairing.Mgr  │
 │  ┌────────┐  │                    │  ┌────────┐  │
 │  │ Pair   │  │                    │  │ Pair   │  │
 │  │ ┌────┐ │◄─┼──── DMSG :PORT ───►│  │ ┌────┐ │  │
 │  │ │CXO │ │  │   (deterministic    │  │ │CXO │ │  │
 │  │ │node│ │  │    pair port)       │  │ │node│ │  │
 │  │ └────┘ │  │                    │  │ └────┘ │  │
 │  └────────┘  │                    │  └────────┘  │
 └──────────────┘                    └──────────────┘
        ▲                                    ▲
        │ visor RPC                          │ visor RPC
        │                                    │
 ┌──────┴───────┐                    ┌───────┴──────┐
 │  skychat     │                    │  skychat     │
 │  (HTTP+UI)   │                    │  (HTTP+UI)   │
 └──────────────┘                    └──────────────┘
```

Each side runs **one CXO node per pair**. The node listens on a
deterministic DMSG port computed from
`SHA256(min(pkA,pkB) || max(pkA,pkB)) mod 50000 + 10000`. Both ends
compute the same port from public information alone, so no
out-of-band port negotiation is needed.

The CXO node hosts **two roles** on the same node:

- **Publisher** — shares this side's outbox feed (feed PK = own
  visor PK), with an allowlist of exactly the peer's PK.
- **Subscriber** — connected to the peer's outbox feed (feed PK =
  peer's visor PK).

One node per pair, two feeds carried on it (own + peer's), addressed
by feed PK. This is materially simpler than running separate
publisher and subscriber CXO nodes per pair on different ports.

## Security model

Three layers, in order of how aggressively they fail:

### Layer 1: Subscriber allowlist (TreeStore)

`treestore.Publisher` exposes an allowlist of permitted subscriber
PKs. The CXO node's `OnSubscribeRemote` hook rejects subscribe
requests from any peer not on the list with a generic `"subscribe
rejected"` error — indistinguishable from "feed doesn't exist," so
a probing peer can't enumerate the allowlist.

For pair feeds, the allowlist is exactly `[peer_pk]`. Even if a
random crawler discovers the visor's pair port, they can't
subscribe and read.

### Layer 2: ECDH + ChaCha20-Poly1305 body encryption

Each pair derives a 32-byte symmetric key from `ECDH(my_sk,
peer_pk)` at Open time. Both sides compute the same key (ECDH
symmetry). Every message body is sealed with ChaCha20-Poly1305
AEAD, with a fresh random 12-byte nonce prepended to the ciphertext.

Even if Layer 1 is bypassed, the leaf bytes are ciphertext.
Tampering is detected via the AEAD tag, so a successful subscriber
can't quietly inject forged messages.

### Layer 3: Transport-level metadata privacy (transport-dependent)

The pair feed runs over whatever transport the visor uses to
connect to the peer's DMSG port. The pairing layer doesn't pick
the transport; the visor's standard transport selection does. So:

- **STCPR (direct PK-to-PK TCP)** — direct visor-to-visor; no
  intermediate node observes the connection metadata.
- **DMSG via untrusted servers** — DMSG servers see "Alice's visor
  opened a stream to Bob's visor on port X at time T." This is
  inherent to public-port DMSG and not specific to chat.
- **Multihop routes** — chat traffic over a 3-hop route exposes
  only "Alice → first hop" and "last hop → Bob" to the respective
  endpoints; the middle is opaque.
- **Multiplexed transports** — when a transport carries multiple
  flows (multiple users, multiple route groups), per-flow timing/
  volume analysis becomes much harder.

So **content privacy is provided by Layers 1+2 inside the chat
layer**, while **metadata privacy depends on the transport
configuration** the visor is running. Strong-anonymity setup =
STCPR + multihop routes; chat inherits whatever level of metadata
hiding the transport gives it.

### What still leaks

Even with all three layers:

- **Path metadata** in the CXO leaf path: `msgs/<unix-nanos>/<seq>`.
  An observer who somehow gets read access still sees timing and
  ordering, just not content.
- **Pair-port confirmation**: anyone who already knows two PKs of
  interest can compute their pair port and confirm whether one
  visor is listening. They can't enumerate contacts from this.
- **Traffic analysis** at the transport layer: nation-state-level
  adversaries observing both endpoints' DMSG traffic can correlate
  ciphertexts. Defending against this requires constant-rate cover
  traffic, which is out of scope for chat alone.

## Pairing handshake (consent-based)

```
Alice                                 Bob
─────                                 ───
POST /pair {bob_pk}
  visor.PairAdd(bob)  [pending]
  send pair-invite ──────────────►   handleConn pair-invite:
                                       pendingPut(alice)
                                       SSE channel=pair-invite (received)
                                       UI shows accept/decline

                                     User clicks Accept:
                                       POST /pair/invites/alice/accept
                                       visor.PairAdd(alice) [pending]
                                       visor.PairMarkActive(alice) [active]
                                       pendingDelete(alice)
                                     ◄───── send pair-ack
handleConn pair-ack:
  visor.PairMarkActive(bob) [active]
  SSE channel=pair-invite (accepted)

(Or, on Decline:
                                     ◄───── send pair-decline
handleConn pair-decline:
  visor.PairRemove(bob)
  SSE channel=pair-invite (declined))
```

The handshake messages (`pair-invite` / `pair-ack` / `pair-decline`)
ride over the **legacy skychat direct path** as small JSON envelopes
(`{"type":"pair-invite"}`). This means the existing CI tests that
exercise legacy plaintext DMs continue to work — the handshake is
identified by the leading `{` byte and dispatched accordingly;
plain-text messages bypass the JSON parser entirely.

The handshake is **consent-based**: a pair-invite no longer auto-
attaches the peer. The invite is held in a pending list and
surfaced via SSE; the user must click Accept or Decline.

## HTTP API

All endpoints require `--pair-enable` (off by default in PR-3
through PR-8; will likely flip after a soak window). When pairing
is disabled, every endpoint returns `503 Service Unavailable`.

### Pair management

| Method | Path                         | Body                  | Effect                                                  |
|--------|------------------------------|-----------------------|---------------------------------------------------------|
| GET    | `/pair`                      | -                     | List pairs (`[]PairInfo` JSON)                          |
| POST   | `/pair`                      | `{"peer_pk":"…"}`     | `visor.PairAdd` + send `pair-invite` over legacy path   |
| DELETE | `/pair/<peer_pk>`            | -                     | `visor.PairRemove` (pair status → revoked)              |
| POST   | `/pair/<peer_pk>/message`    | `{"text":"…"}`        | `visor.PairSend` (publishes one CXO leaf)               |

### Invite management

| Method | Path                                  | Body | Effect                                                          |
|--------|---------------------------------------|------|-----------------------------------------------------------------|
| GET    | `/pair/invites`                       | -    | List pending invites (`[]pendingInvite` JSON)                   |
| POST   | `/pair/invites/<peer_pk>/accept`      | -    | `visor.PairAdd` + `PairMarkActive` + send `pair-ack` to peer    |
| POST   | `/pair/invites/<peer_pk>/decline`     | -    | Drop pending + send `pair-decline` to peer                      |

## SSE schema

Messages on the `/sse` stream are JSON objects with a `channel` field
disambiguating their type:

| `channel`        | Other fields                                         | Meaning                                              |
|------------------|------------------------------------------------------|------------------------------------------------------|
| (absent)         | `sender`, `message`                                  | Legacy plain-text DM (existing behavior)             |
| `"pair"`         | `sender`, `message`, `peer`, `ts`                    | Inbound CXO pair message (drained from `PairPoll`)   |
| `"pair-invite"`  | `event ∈ {received,accepted,declined}`, `peer`, `ts` | Pair handshake state change                          |

## Configuration

Skychat flags (all default off / sensible):

```
--pair-enable           bool       opt-in master switch (default: false)
--pair-rpc              string     visor RPC address (default: localhost:3435)
--pair-poll-interval    duration   inbox drain cadence (default: 1s)
```

Visor side: pairing is auto-initialised by the `pairing` init
module when `dmsgC` is up. State lives at
`<conf.LocalPath>/pairing/`:

```
pairing/
├── pairs.db          bbolt — Records (peer_pk, port, status, …)
└── cxo/
    └── pair/
        └── <peer-pk-hex>/
            ├── cxds.db       CXO content-addressed store
            └── idx.db        CXO index
```

Each pair has its own CXO data directory. Removing a pair (via
`PairRemove`) marks the record revoked but leaves the data dir for
audit; future tooling could expose hard-purge.

## Reserved DMSG port range

Pair feeds use deterministic ports in `[10000, 60000)`. The visor's
CXO user-feed registry rejects ports inside this range so a manually
registered user feed can't shadow a pair publisher.

System DMSG ports (`<300`, plus reserved ranges around 80, 100, 136)
are listed in `pairing.ReservedPorts()`; if the deterministic hash
lands on one, the allocator walks forward to the next free slot —
both sides do the same walk, so the result stays symmetric.

## CLI

`skywire cli visor pair` exposes the visor RPC surface for
scripted setups + diagnostics:

```
skywire cli visor pair                    # list pairs
skywire cli visor pair add <peer-pk>      # register a pair
skywire cli visor pair rm <peer-pk>       # tear down
skywire cli visor pair send <peer-pk> "hello"
skywire cli visor pair poll [--since RFC3339]
```

Useful for testing the pipeline without skychat HTTP, and for
seed-pair lists in integration setups.

## Failure modes

- **Visor RPC unreachable from skychat**: `connectPairRPC` logs and
  disables pairing for the run. All `/pair*` endpoints return 503;
  the UI's `/pair` probe falls back to legacy mode.
- **Peer offline at invite time**: the pair-invite send is best-effort.
  If the legacy direct dial fails, the pair record is kept locally;
  both sides converge later when each calls `PairAdd` (or the peer
  comes online and the invite is re-sent — currently manual; an
  auto-retry sweep is a possible follow-up).
- **Wrong-key decrypt**: AEAD failure. The leaf is silently dropped
  with a debug log; the user sees nothing. This protects against
  mixing up keys after a key-rotation event (currently impossible
  but plausible in the future).
- **Skychat restart during a pending invite**: pending invites live
  in skychat memory only, so a restart loses them. The peer can
  re-invite to recover. Persisting them (e.g. a small file under
  the skychat work dir) is a possible follow-up.

## Roadmap

- **Default-on flag flip** after a soak window confirms stability.
- **Persistent pending invites** so a skychat restart doesn't lose
  outstanding invitations.
- **Auto-retry sweep**: periodically re-try pair-invite sends to
  peers in pending state whose initial dial failed.
- **Group rooms**: a room creator publishes a "members" feed each
  member subscribes to, plus pair-style DMs for the room owner who
  relays into a shared room outbox. The pairing primitives are
  designed to extend cleanly to this.
- **Read receipts / typing indicators**: separate prefix in the
  feed (e.g. `acks/<msg-id>`) so the existing message subscriber
  doesn't surface them as chat content. Optional, low-priority.
