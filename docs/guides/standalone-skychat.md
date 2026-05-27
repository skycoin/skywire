# Standalone skychat with TCP listening

`skywire app skychat` normally runs as a child of `skywire visor` — the
visor hands it its identity, opens its skynet/dmsg listeners, and
mediates pair-RPC. The `--standalone` flag detaches the chat-app from
that lifecycle so it can run independently, survive visor restarts,
and be reached over a direct noise-XK TCP transport (`--tcp-listen`)
in addition to (or instead of) the in-process visor handover.

Two distinct modes:

- **standalone**: chat-app runs as its own process, with no parent
  visor. `--standalone` skips the `PROC_CONFIG` handshake, disables
  the skynet + dmsg listenLoops, and keeps `--tcp-listen` /
  `--tcp-peer` + the HTTP control surface. Reachable via TCP-direct
  only. Pair-RPC endpoints return 503 because there's no visor to
  relay through.

- **embedded (default) + TCP listening**: chat-app runs under a
  visor, with the regular skynet + dmsg listenLoops, AND an
  additional TCP-direct entry point. Other agents can dial in over
  TCP (NAT-pierce, deterministic latency) while the visor-managed
  paths still work.

Both modes need an identity (secret key). The chat-app's identity is
its PK on the network; peers' whitelists pin against it.

## Identity

Provide a secret key by any of:

```bash
# Via config file (preferred — same SK the visor uses, so the chat-app
# advertises the same PK as the visor):
-c /path/to/skywire-config.json

# Via flag (32-byte hex):
--sk <64-hex>

# Via env (same shape as --sk):
export DMSGCURL_SK=<64-hex>
```

If no identity is provided in standalone mode the chat-app generates
a random ephemeral SK at startup — fine for testing, useless for
durable whitelists (the PK changes every restart).

## Standalone with TCP listener (server side)

```bash
./skywire app skychat \
  --standalone \
  --addr 127.0.0.1:8002 \
  --tcp-listen :8801 \
  --tcp-whitelist <peer-pk-1>,<peer-pk-2> \
  -c /opt/skywire/skywire-config.json
```

- `--addr` — local HTTP control surface (history, send, SSE). Keep
  loopback unless you want browser access from another host (then
  use `--addr "*:8002"` and front with a reverse proxy + auth).
- `--tcp-listen :8801` — accept noise-XK on TCP port 8801 from
  whitelisted peers. Bidirectional once established.
- `--tcp-whitelist` — **required** when `--tcp-listen` is set; an
  empty whitelist rejects all connections. Comma-separated PKs.
- `-c skywire-config.json` — identity source.

### Reaching the TCP listener from outside the LAN

`--tcp-listen :8801` binds on all interfaces on the host. To accept
connections from peers on the public internet, the host's NAT/router
needs to forward the chosen port to the host's LAN IP:

- **Home / consumer router**: add a port-forwarding rule that maps
  external TCP port 8801 → `<host-lan-ip>:8801`. Some routers also
  need an inbound firewall rule allowing the external port.
- **VPS / cloud host**: open the port in the cloud provider's
  security group / firewall (AWS SG, GCP firewall, DO firewall,
  Hetzner cloud firewall, etc.). Also check the host's local
  firewall (`ufw allow 8801/tcp`, `firewall-cmd --add-port=8801/tcp`,
  etc.).
- **Tailscale / WireGuard mesh**: bind to the mesh interface
  instead — `--tcp-listen 100.x.y.z:8801` — and skip NAT forwarding
  entirely.

Peers dial in with:

```bash
./skywire cli skychat send \
  --via tcp://<your-pk>@<your-public-ip>:8801 \
  -c /their/skywire-config.json \
  -m "hello"
```

The `<your-pk>` in the URL must match the SK loaded via `-c` (or
`--sk` / env) on the listening side — the noise-XK handshake binds
the connection to that PK.

## Standalone with persistent outbound (client side)

For chat-apps behind NAT that can't accept inbound, use
`--tcp-peer` to dial out to a public-IP peer and hold the
connection open:

```bash
./skywire app skychat \
  --standalone \
  --addr 127.0.0.1:8002 \
  --tcp-peer tcp://<remote-pk>@<remote-host>:8801 \
  -c /path/to/skywire-config.json
```

- `--tcp-peer` may be repeated for multiple peers.
- The chat-app reconnects automatically if the connection drops.
- This side doesn't need a port forwarding rule — it initiates the
  TCP.

## Embedded + TCP listening (both)

Drop `--standalone` and the chat-app re-attaches to the visor while
still serving the TCP-direct entry point:

```bash
./skywire app skychat \
  --addr 127.0.0.1:8001 \
  --tcp-listen :8801 \
  --tcp-whitelist <peer-pk-1>,<peer-pk-2> \
  -c /opt/skywire/skywire-config.json
```

Operators that want the chat-app launched as part of the visor's app
set can wire it in via `skywire-config.json`'s `launcher.apps[]`
block — flags listed in the `args` array. The visor's
`skywire-autoconfig` helper can scaffold this.

## Verifying it's listening

```bash
# Local side — confirm the HTTP control surface is up
curl -s http://127.0.0.1:8002/status | jq

# From a peer host — confirm the TCP listener accepts
nc -zv <your-public-ip> 8801

# Full round-trip — send and wait for ack
./skywire cli skychat send \
  --via tcp://<your-pk>@<your-public-ip>:8801 \
  -c /peer/skywire-config.json \
  --wait 8s \
  -m "ping"
```

`Acked by <pk> in Xms (via tcp)` confirms the noise handshake and
the chat-app's receive pipeline are both functional.

## See also

- [`skywire app skychat`](../skywire/app/skychat/README.md) — full
  flag reference (auto-generated from the cobra tree)
- [Standalone dmsgpty-host](standalone-dmsgpty-host.md) — same
  detached-from-visor pattern for the dmsgpty host
