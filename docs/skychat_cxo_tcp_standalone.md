# CXO-backed standalone skychat over native TCP — design

Status: design / Phase 1 in progress. Owner: Alpha (operator-host agent).

## Goal

Add a **CXO-backed mode** to the standalone skychat: **P2P (1:1) first, then
group chat**. CXO runs over its **own native TCP transport — no dmsg** — keeping
the standalone dmsg-free and matching CXO's standalone heritage. The existing
tcp-direct standalone (noise-XK coordination channel, `--tcp-listen`/`--tcp-peer`)
keeps running unchanged for base coordination.

## Why standalone, not in-visor

The visor-hosted CXO group chat stalled earlier because the participating
**visors restart after each PR merge** (binary rebuild), churning CXO subscriber
state mid-sync. The standalone app is a **separate, operator-controlled process
that does not rebuild/restart on merges** — so building the CXO chat here
sidesteps the exact blocker that stalled the visor-hosted version.

## Architecture (what already exists)

CXO chat machinery is present and transport-pluggable:

- `pkg/cxo/treestore` — `Publisher` / `Subscriber` over a `pkg/cxo/node.Node`.
- `cmd/apps/skychat/pairing` (1:1) and `cmd/apps/skychat/group` (group) Managers
  — each needs only `{transport, SK, DataDir, Logger}`.
- The CXO node **natively supports TCP** (`config.go`: `cfg.TCP.Listen=":8870"`).
  `treestore.NewWithDMSG` deliberately *disables* TCP/UDP/RPC and swaps in a dmsg
  factory (`cxoNode.EnableDMSG`). The TCP analog is: **keep TCP, skip dmsg**.
- **Transport symmetry** is the key enabler:
  - dmsg: `Subscriber.Connect` → `cxoNode.DMSG().ConnectPK(pk)` → `conn.Subscribe(feedPK)`
  - tcp:  `cxoNode.TCP().Connect(address)` → `conn.Subscribe(feedPK)`
  - Both yield the same `*node.Conn` with `.Subscribe(feedPK)`.
  - Only difference: dmsg dials **by PK** (discovery-resolved); TCP dials **by
    explicit `host:port`** — supplied by the operator, exactly like `--tcp-peer`.

## New API surface (`pkg/cxo/treestore`)

- `NewWithTCP(listenAddr string, sk cipher.SecKey, conf PubConfig) (*Publisher, error)`
  — mirror of `NewWithDMSG`: keep `cfg.TCP.Listen = listenAddr`, do **not** call
  `EnableDMSG`, keep the same `OnSubscribeRemote` allowlist hook.
- `Subscriber.ConnectTCP(ctx, address string) error` — mirror of `Connect` but via
  `cxoNode.TCP().Connect(address)`; store the **address** (not the publisher PK) for
  the reconnect watchdog to re-dial.
- `runReconnectWatchdog` — re-dial by stored address when in TCP mode (#2713 silent-
  subscriber recovery carries over unchanged otherwise).
- A shared-node helper so a pair can run publisher + subscriber on one TCP node
  (pairing's existing pattern).

## Phases

1. **Phase 1 — CXO P2P over TCP.** treestore TCP foundation (`NewWithTCP`,
   `ConnectTCP`, watchdog) + a TCP variant of `pairing.Manager` + standalone flags
   (`--cxo`, `--cxo-listen :8870`, `--cxo-peer tcp://pk@host:port`) + in-process pair
   HTTP endpoints (replacing the visor pair-RPC the standalone lacks). **Exit
   criterion:** two standalones reliably 1:1-chat over CXO-TCP, survive a peer
   restart (watchdog re-dial), and replay missed history.
2. **Phase 2 — CXO groups over TCP.** TCP variant of `group.Manager` + group HTTP
   endpoints. Owner/member topology over CXO-TCP.

## Review questions (raised by Gamma)

1. **URL-prefix collision class** (the `isUnderBase` bug): N/A to CXO chat — that
   was HTTP `/all-transports` CLI feed-routing in `cmd/.../rpc/root.go`. CXO chat
   addresses by **feed PK + path prefix** (`msgs/<ts-nano>/<seq>`), with explicit
   per-pair / per-group feed PKs (pair feed deterministic; group feed = owner PK).
   **Action:** audit `Subscriber.matchesPrefixLocked` / `SetPrefixes` for the same
   loose-prefix class — require a path-segment boundary, not a raw substring.
2. **In-visor vs standalone:** standalone, new sub-mode (per Moses). Preserves the
   dmsg-free + restart-stable property that unblocks it.
3. **Silent-subscriber recovery:** inherited from treestore — `runReconnectWatchdog`
   (#2713) + `ConnectAndWaitForRoot` + history replay. In TCP mode the watchdog
   re-dials the stored TCP address; no extra heartbeat needed beyond the quiet-
   threshold re-Connect.

## Coordination / ops

- Current tcp-direct standalone stays up for coordination; operator port-forwards
  the extra CXO-TCP port(s) (`:8870`…).
- Reuse the same `-c skywire-config.json` identity (sk) so the CXO node PK == the
  coordination identity.
- Agents review after the 2026-05-31 reward recovery and `fix/cxo-url-boundary-match`
  land.
