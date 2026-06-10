# Skywire Skychat

**Skychat** is skywire's messaging app: direct messages, group chat, and a
pairing handshake between two keys — addressed by **public key**, carried
over skynet, dmsg, a direct noise-XK TCP transport, or CXO feeds. Like the
[pty](../pty/README.md), it's one subsystem reached through two command
structures:

- **`skywire app skychat`** — the **server app**. One binary; its *mode* is
  chosen by **flags** (not subcommands): which transports it listens on,
  whether it runs under a visor or `--standalone`, and whether CXO feeds
  are enabled.
- **`skywire cli skychat …`** — the **client**: `send`, `listen`, `group`,
  `pair`, `history`, `events`, `chat` (TUI), `alias`, `status`.

A lighter, separate tool — **`skywire cli dmsg chat`** — is a standalone
interactive chat over dmsg with no visor and no app; handy for a quick
one-off conversation, distinct from the `app skychat` server.

## The modes

Modes are flag-selected on `app skychat`; several combine.

| Mode | Identity | Transport(s) | Run it with | Use when |
|------|----------|--------------|-------------|----------|
| **visor-hosted** (default) | the **visor's** key | dmsg + skynet (`--dmsg`/`--skynet`, both on) | launched by the visor; driven by `cli skychat` | the node runs a visor — the default, fleet case |
| **standalone** | `-c`/`--sk`/env (no visor key handover) | **TCP-direct only** | `app skychat --standalone …` | you want a chat process **independent of the visor lifecycle** — survives restarts; reachable via TCP-direct |
| **TCP-direct** (transport) | `-c`/`--sk`/env | direct noise-XK TCP | `--tcp-listen :8800` / `--tcp-peer tcp://pk@host:port`; client `send --via tcp://…` | NAT-pierce, deterministic latency, survives dmsg outages — works **embedded or standalone** |
| **CXO-backed** | `-c`/`--sk`/env | native TCP CXO feeds (no dmsg) | `--cxo` (DM) / `--cxo-group …` (groups), `--cxo-listen :8802`, `--cxo-peer …` | restart-stable DM/group chat; backs `cli skychat group`. Works in `--standalone`. |

Detailed docs:

- **[skychat-visor.md](skychat-visor.md)** — the visor-hosted app and the
  `cli skychat` client. **Start here.**
- **[standalone-skychat.md](standalone-skychat.md)** — `--standalone`,
  TCP-direct, and CXO (DM + groups).

## The trust model

Identity is a **secret key**; the derived **PK** is the address peers pin.
Provide it with `-c <skywire-config.json>` (reuses the visor's SK → same
PK as the visor), `--sk <hex>`, or `DMSGCURL_SK`. In standalone with no
identity, a **random ephemeral SK** is generated each start — fine for
testing, useless for durable whitelists.

- **TCP-direct**: a noise-XK handshake pins the server PK; `--tcp-whitelist`
  is the `authorized_keys` (empty = open to any authenticated key).
- **CXO**: feeds are addressed by feed PK; group rosters are owner-signed;
  `--cxo-peer` lists who you subscribe to.
- **HTTP control surface**: localhost by default; `--password-file` (bcrypt)
  gates it if exposed; the hypervisor reverse-proxies it with
  `--internal-token`.

## Features built on skychat

- **`group` — owner-centric group chat** over CXO feeds: `create`, `invite`
  /`join`, `send`/`listen`, `history`, `promote`/`demote`/`leave`,
  `list`/`info`. `public` or `private` (AES key shipped in the invite link).
- **`pair` — per-partner pairing**: `--pair-enable` brings up per-partner
  CXO pair feeds (HTTP `/pair` + handshake). `pair add` (request),
  `accept`/`decline`, `send`, `list`, `remove`. **Note:** `pair poll` has
  no SSE — you **poll** for replies (`--since`/`--limit`).
- **`events` — agent-friendly stream**: structured **NDJSON** chat events —
  the scripting/automation surface (skychat's analog of `pty exec`).
- **`history` — persisted store**: optional BoltDB (`--persist…`) with
  per-peer caps, TTL, rate-limit, and SSE seed.
- **`alias`** — local PK→name aliases. **`chat`** — a bubbletea split-pane
  TUI.

## Ports (defaults / conventions)

| Port | What |
|------|------|
| `:8001` | visor-hosted HTTP control surface (`--addr`) |
| `:8002` | standalone HTTP control surface (convention) |
| `:8800` / `:8801` | TCP-direct (`--tcp-listen`; guides commonly forward `:8801`) |
| `:8802` | CXO feed listener (`--cxo-listen`) |

> **SSE-hub gotcha.** Each HTTP control surface has its **own SSE hub**. A
> message that lands on the visor-managed `:8001` hub does **not** appear on
> a standalone `:8002` hub, and vice versa. If you run both, `listen` on
> **both** or you'll miss messages. DMs sent through a visor route land on
> `:8001`, not the standalone surface.

## Full command map

### `skywire cli skychat …` — the client
| Command | Role |
|---------|------|
| `cli skychat send -t <pk> -m <msg> [-n skynet\|dmsg] [--via tcp://…] [-w 5s]` | send a DM (ack with `--wait`; `--via` bypasses the local app over TCP) |
| `cli skychat listen [--from <pk>] [-n net]` | stream incoming messages (SSE) |
| `cli skychat chat [-t <pk>] [-n net]` | interactive TUI |
| `cli skychat events` | structured NDJSON event stream |
| `cli skychat history` | print persisted history |
| `cli skychat status` | app counters / health |
| `cli skychat alias add\|ls\|rm` | local PK aliases |
| `cli skychat group create\|invite\|join\|send\|listen\|history\|list\|info\|add\|promote\|demote\|leave\|delete` | group chat |
| `cli skychat pair add\|accept\|decline\|send\|poll\|list\|remove` | pairing (poll, no SSE) |

### `skywire app skychat` — the server
One command, mode chosen by flags: `--standalone`, `--dmsg`/`--skynet`,
`--tcp-listen`/`--tcp-peer`/`--tcp-whitelist`, `--cxo`/`--cxo-group`/
`--cxo-listen`/`--cxo-peer`, `--pair-enable`, `--persist…`, `--addr`,
`-c`/`--sk`, `--password-file`. See [standalone-skychat.md](standalone-skychat.md)
and `skywire app skychat --help`.

### `skywire cli dmsg chat`
Standalone interactive chat over dmsg, no visor — a separate lightweight tool.

## See also

- [skychat-visor.md](skychat-visor.md) — visor-hosted app + `cli skychat`
- [standalone-skychat.md](standalone-skychat.md) — standalone / TCP-direct / CXO
- [../pty/README.md](../pty/README.md) — the parallel pty subsystem
