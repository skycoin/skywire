# Skychat Refactor RFC

status: draft
date: 2026-07-02
supersedes-in-part: docs/skychat_group_gossip_rfc.md (federation), docs/skychat_cxo_tcp_standalone.md (standalone)
related: pkg/cxo/treestore, pkg/servicedisc, pkg/visor/visorcore (convergence pattern)

## 1. Problem

Skychat grew feature-by-feature without a shared core. The result works but is
hard to make *robust* because the same logic exists two or three times and the
copies drift:

- **Three parallel implementations.** The native app (`cmd/apps/skychat/commands/skychat.go`,
  ~2160 lines), the browser-tab wasm reimplementation (`cmd/wasm-visor/skychat_js.go`),
  and the visor-side RPC wrapper (`pkg/visor/group.go` + friends). The
  length-prefixed frame protocol (4-byte big-endian length, 64 KiB cap,
  `chat-msg`/`chat-ack` envelopes) is written out **three times**, each with its
  own constant and write-mutex.
- **Three message models.** Framed 1:1 (no struct, arrival-ordered), pair-CXO
  (`Message{Text,TS}`), group-CXO (`Message{SenderPK,TS,Text,Ciphertext,Nonce,Signature}`).
  No shared identity or ordering.
- **Two group generations coexist** in one package: the original owner-relay
  (owner offline ⇒ read-only SPOF) and the newer federated per-member peerSubs
  mesh, with dead legacy fields kept for parse-compat. `group/session.go` is
  ~2400 lines, `group/manager.go` ~1430 — each mixing state, crypto, transport,
  and reconnect state machines in one type.
- **Five transports** (dmsg, skynet routes, TCP-direct, CXO-native-TCP, `--via`),
  each with its own send/receive plumbing.
- **Ordering is wall-clock naive** everywhere; dedup is a best-effort bounded set.
- **No discovery.** You can only join a group whose members you already know.
- **Global mutable state** (package-level conns, hub, counters, flags registered twice).

This is the same divergence class the visor itself had before the `visorcore`
convergence (native vs wasm drifting apart, the #3277 bug family). The fix is the
same: extract one shared, platform-neutral core; make the runtimes thin adapters.

## 2. Goals / non-goals

Goals:
1. One shared `pkg/skychat` core used by native app AND wasm visor — no reimplementation.
2. One message model + one wire codec (delete the three framing copies).
3. One group model (federated), owner-relay demoted to a bootstrap, not a SPOF.
4. Transport behind a single interface; the five transports become adapters.
5. **Public group discovery** via the existing service-discovery (`type=skychat`).
6. Causal-ish ordering so display order stops flipping under clock skew.
7. skychat as a first-class desktop window in the wasm mini-desktop (done, §7).

Non-goals (for this RFC):
- Replacing CXO/treestore as the group data plane (it stays).
- A DHT for discovery (SD-first; DHT is a later swap behind the same interface).
- Changing the on-the-wire crypto (per-message secp256k1 signatures stay).

## 3. Proposed package layout

Mirror the visorcore move — a shared core, thin adapters, build-tag splits for
the platform-hostile pieces (exactly the CXO cxds/idxdb `!js` / `js` split used
for the in-memory telemetry publisher).

```
pkg/skychat/
  message/     ONE model {ID, SenderPK, TS, Seq, Body, Sig} + ONE wire codec
               (4-byte length prefix, 64 KiB cap, chat-msg/chat-ack envelopes).
               Replaces framedConn (native), chatConn (wasm), RelayMessage framing.
  session/     peer/group session state-machine (data + methods), transport-agnostic:
               depends on a small Conn/Dialer interface, not on dmsg/app-client/TCP.
  group/       group record + roster + signed gossip. Federated-only. Owner-relay
               kept only as an optional bootstrap listener, never required for liveness.
  discovery/   NEW: SD-backed public-group directory (§6). PublishGroup / ListPublicGroups /
               JoinPublic over servicedisc.Client.
  store/        history behind an interface; bbolt (native, //go:build !js) +
                in-memory ring (wasm, //go:build js). Same pattern as cxds/idxdb.

cmd/apps/skychat            → thin adapter: core + app-client transport + HTTP/SSE UI + bbolt store
cmd/wasm-visor/skychat_js.go → thin adapter: SAME core + DmsgNetworker + in-memory store + JS hooks
pkg/visor/*group*/*pairing*  → RPC surface over the core (unchanged externally)
```

Transport abstraction: one `Transport` interface (`Dial(pk, port) (Conn, error)`,
`Listen(port) (Listener, error)`), with adapters:
`appclient` (native, over the visor app surface), `dmsgdirect` (wasm DmsgNetworker),
`tcpdirect`, `cxotcp`. "Five ways a message travels" become five adapters behind
one interface — accept-interface-at-the-consumer, per the Go coding standard (K7).

Applies the standard's kernel directly: K2 (one owning type per shared collection,
no package globals), K6 (build-tag platform splits), K7 (small consumer-side
interfaces), and the visorcore convergence precedent.

## 4. Message model & ordering

One struct:

```go
type Message struct {
    ID       MessageID   // deterministic: hash(SenderPK, Seq) — stable across resend
    SenderPK cipher.PubKey
    Room     RoomID      // zero value = 1:1 DM (peer is the other endpoint)
    TS       int64       // sender wall-clock, unix nanos (display hint only)
    Seq      uint64      // per-sender monotonic counter (authoritative within a sender)
    Body     []byte      // UTF-8 text (or, for private groups, ciphertext)
    Sig      []byte      // secp256k1 over the canonical encoding
}
```

Ordering: today it is wall-clock `msgs/<ts-nanos>/<seq>`, which reorders under
skew. Proposal: keep `(SenderPK, Seq)` as the authoritative per-sender order, and
for cross-sender display use a **small bounded reorder buffer** sorted by
`(TS, SenderPK, Seq)` with a short grace window (e.g. 2s) before commit. This is
not a full CRDT/vector clock — it is a pragmatic "sort within a window" that
removes the visible flip without a consensus protocol. A vector-clock upgrade can
land later behind the same `Message` shape (Seq already carries per-sender causality).

Dedup: content-identity by `ID` into the existing bounded `recentSet`.

## 5. Group model (federated)

Collapse the two generations to **federated-only**:
- Every member (owner included) publishes to its **own** CXO feed.
- Each session holds one `treestore.Subscriber` per other member (peerSubs).
- Roster/admin changes are signed `RosterMutation`/`AdminMutation` gossiped over
  feed prefixes and reconciled per-visor (as today, `group/gossip.go`).
- Owner-relay (`Record.Port + 1` listener) is retained ONLY as an optional
  bootstrap/fallback path; group liveness never depends on the owner being online.

This removes the documented owner-offline SPOF and deletes the dead
mirror-prefix / legacy `sub` field carried for parse-compat.

## 6. Public group service discovery (the new capability)

A public group is just a new **service type** in the existing service-discovery.
No new service, no new infrastructure.

Add to `pkg/servicedisc/types.go`:

```go
ServiceTypeSkychat = "skychat"
```

### Publish
A visor hosting a *public* group registers one SD entry per room:
- `type: skychat`
- `address: <ownerPK>:<port>` — the feed/relay entry point
- metadata: `group_id` (uuid), `name`, `topic`, `mode: public`, `feed_pk`, `members` (count)

SD entries are keyed by the advertiser's PK, so "who hosts this room" is
authenticated for free — no separate signing needed for the directory entry.

### Discover
Any client queries the same endpoint the wasm proxy panel already uses for
`type=proxy`:

```
GET sd.dmsg /api/services?type=skychat
→ [{ address: "<ownerPK>:<port>", group_id, name, topic, mode, members }, …]
```

The UI list pattern already exists (`browse.js` populates the skysocks dropdown
from `?type=proxy`); a "public rooms" list reuses it verbatim.

### Join
1. Pick a room from the directory.
2. Subscribe to the owner's CXO feed (public ⇒ read is open).
3. To write: publish to your own feed; roster gossip auto-admits in public mode
   (vs owner-allowlist in private mode).
4. Roster gossip propagates the new member to everyone.

### Public-mode considerations
- **Moderation.** Public = anyone joins = spam risk. Reuse the existing signed
  roster eviction (`SetAllowlist` / `AdminMutation`) for bans; add per-sender
  rate limiting; optionally a "read-only until approved" tier.
- **Encryption.** Private groups share an AES key out-of-band. Public rooms run
  either unencrypted (it is public anyway) or with a *published* room key; either
  way per-message signatures stay for authenticity.
- **Trust.** SD is a semi-central *directory* (like a tracker/DNS); the chat data
  plane stays P2P/federated over CXO. Matches skywire's overall model, and a
  future Kademlia DHT can replace SD-for-discovery behind the `discovery`
  interface without touching the data plane.

`pkg/skychat/discovery` API:

```go
PublishGroup(ctx, GroupInfo) error       // owner advertises a public room
ListPublicGroups(ctx) ([]GroupInfo, error) // client browses the directory
JoinPublic(ctx, GroupInfo) (*Session, error)
```

## 7. Wasm desktop chat window (landed with this RFC)

`pkg/wasmhv/browseui/browse.js` gains `createChatWindow` — a 1:1 skychat client
as a WinBox desktop window, the missing peer to the skynet browser. It drives the
two existing wasm-visor JS hooks:
`skychatSend(peerPkHex, text) → Promise` and `skychatMessages() → JSON [{from,text,ts,out}]`.
Distinct `from` PKs in the buffer are surfaced as clickable chips, so an incoming
message from an unknown peer is discoverable without knowing their key first.
Gated on `skywireVisor.skychatSend` (wasm-visor only; native keeps its Angular
skychat tab), exactly like the `host` window. Added to the ☰ app menu as `chat`.

This is deliberately a thin client over the current wasm hooks; once §3 lands, it
re-points at the shared core (groups + discovery) with no UI rewrite.

## 8. Sequencing

1. **Wasm desktop chat window** — done (§7).
2. **`pkg/skychat/message` + wire codec** — collapse the 3 framing copies; lowest-risk
   structural win; native + wasm adopt it behind the `Transport` interface.
3. **`ServiceTypeSkychat` + `pkg/skychat/discovery`** + a "public rooms" list in the
   wasm desktop and the Angular tab — the discovery feature (§6).
4. **Collapse group generations** to federated-only; decompose the god files
   (`commands/skychat.go`, `group/session.go`, `group/manager.go`).
5. **Reorder buffer** for causal-ish display ordering (§4).

Each step is independently shippable and leaves the app working.

## 9. Open questions

- Public-room key distribution: published room key vs plaintext — per-room policy flag?
- Should SD carry a coarse `members`/`activity` count (staleness vs usefulness)?
- Rate-limit budget for public rooms — per-sender, per-room, or both?
- Do we advertise 1:1 "reachable for DM" presence in SD too, or only group rooms?
