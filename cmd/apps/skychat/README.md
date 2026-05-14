# Skywire Chat (skychat)

`skychat` is an app that runs alongside the skywire visor and
provides messaging between visors over dmsg and/or skywire routes.
It exposes a local HTTP server (default `127.0.0.1:8001`) for the
browser UI plus the `skywire cli skychat` family of subcommands for
headless / scripted use.

This README focuses on what the app does and how to drive it from the
CLI. For the per-pair encrypted CXO layer, see
[docs/skychat_pairing.md](../../../docs/skychat_pairing.md).

## Quick start

`skychat` is auto-launched by the visor when present in the apps
list. A typical entry in `skywire-config.json`:

```jsonc
{
  "app": "skychat",
  "auto_start": true,
  "port": 1
}
```

The HTTP UI lands on `http://127.0.0.1:8001` once the visor starts.

## Sending a message (CLI)

```bash
skywire cli skychat send -t <peer-pk> -m "hello"
```

Default semantics (as of 2026-05-14): the command waits up to 5
seconds for the peer's chat-app to acknowledge receipt. Output:

- Acked: `Acked by <pk> in <N>ms (id=<uuid>)`, exit 0.
- Timeout: `send to <pk> via <net> not acked: <reason>`, exit 1.

Override the wait with `--wait 0` for fire-and-forget (returns
success as soon as the local visor's WriteFrame succeeds — useful
against peers on pre-2026-05-12 binaries that can't ack).

Other flags:

- `--net skynet|dmsg` — choose network. Default `skynet`.
- `--retries N` — HTTP/transport retry count. Default 1.
- `--wait DURATION` — peer-ack wait (server-clamped [100ms, 60s]).

## Listening for inbound messages

```bash
skywire cli skychat listen
```

Streams every inbound DM as one JSON event per line. Output shape:

```
[<sender-pk>/<net>] <body>
```

Flags:

- `--from <PK>` — filter by sender.
- `--net skynet|dmsg` — filter by transport.
- `--raw` — emit unescaped multi-line bodies (humans only).
- `--json` — full JSON event per line (machine-readable).

The listener uses SSE under the hood (`GET /sse` on the chat-app's
HTTP server). It auto-reconnects on visor restart and replays
recent messages from a 256-message ring buffer so brief disconnects
don't lose data.

## Group chat

```bash
# Owner: create a group, get an invite link
skywire cli skychat group create my-room
skywire cli skychat group invite <group-id>

# Member: join via the invite link
skywire cli skychat group join <invite-link>

# Send / read
skywire cli skychat group send <group-id> "hi everyone"
skywire cli skychat group listen
skywire cli skychat group info <group-id>
skywire cli skychat group list
```

Groups are built on top of CXO TreeStore feeds. The owner publishes
the canonical group feed; members subscribe and (post-#2539)
publish their own per-member feed. See `cmd/apps/skychat/group/`
for the implementation.

## Message history

When persistence is enabled, the chat-app stores every inbound and
outbound message in a local SQLite database. Recover from
listener-side missed events:

```bash
skywire cli skychat history --limit 50
skywire cli skychat history --peer <pk> --since 1h
```

Flags:

- `--peer <PK>` — only one peer's messages.
- `--limit N` — max messages, default 100, server cap 1000.
- `--since DURATION` — drop messages older than (e.g. `1h`, `24h`,
  `168h` for a week).
- `--json` — NDJSON output, one event per line.

Enable persistence via skychat flags (set in the app's `args` in
the visor config):

```jsonc
{
  "app": "skychat",
  "args": [
    "--persist-enable",
    "--persist-db", "/var/lib/skywire/skychat-history.db",
    "--persist-ttl-days", "30"
  ]
}
```

See `commands/skychat.go` for the full persistence flag list.

## Health & introspection

The chat-app exposes a `/status` endpoint:

```bash
curl -s http://127.0.0.1:8001/status | jq
```

Key fields:

| field | meaning |
|---|---|
| `app_uptime_sec` | seconds since the app started |
| `inbound_msg_count` | DMs successfully decoded |
| `outbound_msg_count` | DMs successfully written |
| `inbound_drop_count` | ReadFrame errors |
| `outbound_fail_count` | sends that gave up after retry |
| `outbound_retry_count` | sends that took the redial-after-stale-conn path |
| `sse_drop_count` | broadcasts where a subscriber's buffer was full OR no subscribers were connected |
| `sse_subscribers` | live SSE listeners right now |
| `active_peer_conns` | chat-app **framed connections** this app holds (NOT a dmsg session count — see below) |
| `peers` | PKs of the active_peer_conns |
| `last_rx_ts` / `last_send_ts` | RFC3339 timestamps of last successful inbound/outbound |

**Caveat on `peers` / `active_peer_conns`**: these count chat-app
framed connections, not underlying dmsg sessions. After a visor
restart this starts at 0 and only grows when this app initiates an
outbound DM or accepts an inbound one. Underlying dmsg may be
fully reachable while these read 0 — for example, a probe via
`cli skychat send --wait 5s` will succeed and populate the counter
as a side effect.

## Operational notes

### Running headless

The default listen address is `127.0.0.1:8001` (localhost-only).
For a multi-machine setup where the listener runs on a different
host:

```jsonc
{
  "app": "skychat",
  "args": ["--addr", "0.0.0.0:8001"]
}
```

Gate the HTTP endpoints with basic auth via `--password-file`:

```jsonc
{
  "app": "skychat",
  "args": [
    "--addr", "0.0.0.0:8001",
    "--password-file", "/etc/skywire/skychat-auth.htpasswd"
  ]
}
```

The file contains a bcrypt hash (any single line); when set, every
HTTP endpoint requires matching basic auth. The hypervisor's
reverse proxy bypasses the gate via a per-process internal token
the visor sets automatically.

### Listener supervision

A robust listener loop for headless agents:

```bash
while true; do
  skywire cli skychat listen
  sleep 1
done >> /var/log/skychat.log 2>&1
```

The CLI auto-reconnects internally on SSE drops; the outer loop
catches the (rare) hard exit. The 256-message replay buffer on the
chat-app side means a brief restart window doesn't lose messages.

### Frame protocol versioning

`/status` exposes `frame_proto_version` so operators can diagnose
staggered-deploy version skew before it manifests as confusing wire
failures. The current frame protocol is version `1` (length-prefixed
frames, since #2504).

## Architecture

- HTTP server on `--addr` serves the browser UI, `/status`, `/sse`
  (listener stream), `/message` (send), `/history`, and pair-control
  endpoints when pairing is on.
- DM messages are length-prefixed framed connections (4-byte
  big-endian length + payload, max 64 KiB per frame).
- Group messages are published over CXO TreeStore feeds.
- The app talks to the visor via `pkg/app` — it does NOT speak
  directly to dmsg or the router; everything routes through the
  visor's app surface.

See:

- `commands/skychat.go` — main app + framed-conn protocol
- `group/` — group chat (TreeStore-backed)
- `history/` — SQLite persistence layer
- `pairing/` — per-pair CXO encryption (see also
  [docs/skychat_pairing.md](../../../docs/skychat_pairing.md))

## Two-visor local development

For local testing, two visors on the same host, each with skychat
on a different HTTP port:

`skywire1-config.json`:
```jsonc
{
  "apps": [
    { "app": "skychat", "auto_start": true, "port": 1 }
  ]
}
```

`skywire2-config.json`:
```jsonc
{
  "apps": [
    {
      "app": "skychat",
      "auto_start": true,
      "port": 1,
      "args": ["--addr", "127.0.0.1:8002"]
    }
  ]
}
```

Build and run:

```bash
go build -o ./build/apps/skychat.v1.0 ./cmd/apps/skychat
go build -o ./build/skywire .
./build/skywire visor -c skywire1-config.json &
./build/skywire visor -c skywire2-config.json &
```

UI on http://127.0.0.1:8001 (visor 1) and http://127.0.0.1:8002
(visor 2). Each visor's PK is in its config; use those PKs as the
`-t <peer-pk>` argument when sending between them.
