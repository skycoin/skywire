# Standalone skychat — TCP-direct & CXO

The visor-hosted app ([skychat-visor.md](skychat-visor.md)) lives and dies
with the visor. The **`--standalone`** flag detaches the chat-app so it
runs as its **own process** — it survives visor restarts and is reached
over a direct noise-XK **TCP** transport and/or **CXO** feeds.

This is the right tool when chat must stay up across visor rebuilds (e.g.
fleets where every PR merge restarts the visor), or for a NAT-pierced,
deterministic-latency channel that doesn't depend on dmsg.

## The two ways to run it

- **standalone** (`--standalone`): no parent visor. Skips the
  `PROC_CONFIG` handshake, **disables the skynet + dmsg listenLoops**, and
  keeps `--tcp-listen`/`--tcp-peer` + CXO + the HTTP control surface.
  Reachable via **TCP-direct/CXO only**. Pair-RPC endpoints return **503**
  (no visor to relay through).
- **embedded + TCP/CXO**: drop `--standalone` and the app re-attaches to
  the visor (skynet+dmsg as usual) **and** serves the extra TCP/CXO entry
  points. Best of both, but tied to the visor lifecycle.

## Identity

Every mode needs a secret key; the derived PK is the address peers pin:

```bash
-c /opt/skywire/skywire-config.json   # preferred — same SK as the visor → same PK
--sk <64-hex>                         # explicit
export DMSGCURL_SK=<64-hex>           # env (same shape as --sk)
```

No identity in standalone → a **random ephemeral SK** each start (PK
changes every restart; useless for durable whitelists).

## TCP-direct

### Server side (accept inbound)

```bash
skywire app skychat \
  --standalone \
  --addr 127.0.0.1:8002 \
  --tcp-listen :8801 \
  --tcp-whitelist <peer-pk-1>,<peer-pk-2> \
  -c /opt/skywire/skywire-config.json
```

- `--addr` — local HTTP control surface (history, send, SSE). Keep it
  loopback unless you intend browser access (`--addr "*:8002"` + reverse
  proxy + `--password-file`).
- `--tcp-listen :8801` — accept noise-XK on TCP; bidirectional once
  established.
- `--tcp-whitelist` — the `authorized_keys`. Empty = **open to any
  authenticated key** (matches the skynet/CXO convention); set it to pin
  specific peers.

Peers dial in with the client's `--via`:

```bash
skywire cli skychat send \
  --via tcp://<your-pk>@<your-public-ip>:8801 \
  -c /their/skywire-config.json -w 8s -m "ping"
```

The `<your-pk>` in the URL must match the SK on the listening side — the
noise-XK handshake binds the connection to that PK.

### Client side (dial out from behind NAT)

For a host that can't accept inbound, hold a persistent outbound link:

```bash
skywire app skychat \
  --standalone --addr 127.0.0.1:8002 \
  --tcp-peer tcp://<remote-pk>@<remote-host>:8801 \
  -c /path/to/skywire-config.json
```

`--tcp-peer` is repeatable and auto-reconnects; this side needs no port
forward (it initiates the TCP).

### Exposing the TCP port outside the LAN

`--tcp-listen :8801` binds all interfaces; to accept from outside the LAN:

- **Home router:** forward external TCP `8801` → `<host-lan-ip>:8801`.
- **VPS / cloud:** open the provider firewall **and** the host firewall
  (`ufw allow 8801/tcp`, `firewall-cmd --add-port=8801/tcp --permanent`).
- **Mesh (Tailscale/WireGuard/ZeroTier):** bind the mesh interface —
  `--tcp-listen 100.x.y.z:8801` — and skip NAT forwarding.

## CXO-backed (DM + groups, no dmsg)

CXO runs over its **own native TCP transport** — keeping the standalone
dmsg-free and restart-stable. The tcp-direct channel above can keep running
alongside for coordination; CXO carries the durable, replayable messaging.

### Direct messages (P2P)

```bash
skywire app skychat \
  --standalone --addr 127.0.0.1:8002 \
  --cxo \
  --cxo-listen :8802 \
  --cxo-peer tcp://<peer-feedpk>@<peer-host>:8802 \
  -c /opt/skywire/skywire-config.json
```

- `--cxo` — publish your outbound to your CXO feed, subscribe to
  `--cxo-peer` feeds. A reconnect watchdog re-dials the stored address and
  replays missed history on recovery.
- `--cxo-listen :8802` — where peers dial your feed (forward this port like
  the tcp-direct one).
- `--cxo-peer tcp://<feedpk>@host:port` — repeatable; who you subscribe to.

### Federated groups

```bash
skywire app skychat \
  --standalone --addr 127.0.0.1:8002 \
  --cxo-group <group-id> \
  --cxo-group-owner <owner-pk> \
  --cxo-listen :8802 \
  --cxo-peer tcp://<member-feedpk>@<host>:8802 \
  -c /opt/skywire/skywire-config.json
```

- `--cxo-group <id>` — enable federated group chat (roster, signing,
  gossip) over native TCP.
- `--cxo-group-owner <pk>` — the owner PK. **Your role is owner** if it
  equals your identity, else **member**.
- Members come from `--cxo-peer`. Drive the group from the client with
  `cli skychat group …` ([skychat-visor.md](skychat-visor.md)).

## Verified walkthrough: two instances on one host

The smallest end-to-end check, run on 2026-09-08 against the develop build
(v1.3.94-0). Two chat-apps on loopback, each with its own identity, one
listening and one dialing, a message each way over TCP-direct.

Mint two identities (`gen-keys` prints the public key on the first line and
the secret key on the second):

```bash
skywire cli config gen-keys > keysA.txt
skywire cli config gen-keys > keysB.txt
```

Start A listening, pinned to B, and B dialing A. Neither needs a visor. The
HTTP control surfaces sit on 8801 and 8802; the TCP-direct listener on 8800:

```bash
skywire app skychat --standalone --sk 1000 3 10 983 1000sed -n 2p keysA.txt) --addr 127.0.0.1:8801 --tcp-listen 127.0.0.1:8800 --tcp-whitelist 1000 3 10 983 1000sed -n 1p keysB.txt)
skywire app skychat --standalone --sk 1000 3 10 983 1000sed -n 2p keysB.txt) --addr 127.0.0.1:8802 --tcp-peer tcp://1000 3 10 983 1000sed -n 1p keysA.txt).0.0.1:8800
```

A logs `tcp-direct conn accepted from <pkB>` and B logs `tcp-peer connected
to <pkA>.0.0.1` within a second. Both `/status` endpoints then report
`"active_peer_conns":1`.

Send a message from each side. `wait_ms` makes the call block until the
peer acknowledges, and the response carries the message id:

```bash
curl -s -X POST http://127.0.0.1:8801/message -H 'Content-Type: application/json' \
  -d '{"recipient":"<pkB>","message":"hello from A over tcp-direct","wait_ms":3000}'
# {"acked":true,"id":"8077ca12ee05c806","ms":2}
curl -s -X POST http://127.0.0.1:8802/message -H 'Content-Type: application/json' \
  -d '{"recipient":"<pkA>","message":"hello from B over tcp-direct","wait_ms":3000}'
# {"acked":true,"id":"490cd10ae04ff9c7","ms":1}
```

Every message carries an id whether or not you wait for it. The receiving
side sees it on its SSE stream (`curl -N http://127.0.0.1:8802/sse`):

```
data: {"dir":"in","id":"8077ca12ee05c806","len":28,"message":"hello from A over tcp-direct","network":"tcp-direct","sender":"<pkA>"}
```

and the sending side sees its own `"dir":"out"` event followed by a
`dm-status` event with `"status":"received"` once the peer confirms. The
`/status` counters end at one inbound and one outbound message on each side
with `outbound_fail_count` still zero.

Two things people get wrong:

- The peer address is `tcp://<pk>:port`. The scheme is mandatory; a
  bare `<pk>:port` is rejected with `tcp-peer: must start with
  "tcp://"`.
- `/history` answers `persistence not enabled` unless `--persist` is on.
  Without it, the SSE stream is the only place inbound messages appear.

## Persisted history

Off by default; enable a local BoltDB:

```bash
  --persist --persist-db /var/lib/skychat/history.db \
  --persist-per-peer-cap 500 --persist-ttl 30 --persist-seed 50 \
  --persist-whitelist /etc/skychat/persist-peers.txt
```

Per-peer FIFO cap, per-minute rate limit, total-size cap, TTL sweep, and a
seed count that backfills new SSE clients.

## Verifying

```bash
curl -s http://127.0.0.1:8002/status | jq          # HTTP control surface up?
nc -zv <your-public-ip> 8801                        # TCP listener reachable?
skywire cli skychat send --via tcp://<your-pk>@<your-public-ip>:8801 \
  -c /peer/skywire-config.json -w 8s -m "ping"      # full round-trip
```

`Acked by <pk> in Xms (via tcp)` confirms the handshake and receive
pipeline.

> Remember the **SSE-hub split**: this standalone's `:8002` hub is separate
> from a visor's `:8001` hub. Run `cli skychat listen` against the surface
> you actually want to read, or both. (See [README.md](README.md).)

## See also

- [README.md](README.md) — the model, all modes, ports, SSE-hub gotcha
- [skychat-visor.md](skychat-visor.md) — visor-hosted app + `cli skychat`
- `skywire app skychat --help` — full, current flag reference
