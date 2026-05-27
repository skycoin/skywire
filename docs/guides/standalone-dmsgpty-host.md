# Standalone dmsgpty-host with TCP listening

`skywire dmsg pty host` runs a dmsgpty server independently of
`skywire visor`. The visor embeds its own dmsgpty Host (per
`skywire-config.json` → `dmsgpty.dmsg_port`), but the standalone
binary is useful when:

- the operator wants pty access on a host that doesn't run a visor
  (jump box, bastion, embedded device);
- the visor's dmsgpty would compete with another visor process on
  the same dmsg port;
- the operator wants a TCP-only fallback (no DMSG dependency) for
  emergency-recovery access when DMSG is unreachable.

Three modes:

- **DMSG-only**: defaults. Listens on dmsg at the configured port
  (default 22). Reachable from peers via `skywire cli dmsg pty exec`.
- **DMSG + TCP**: add `--tcplisten :PORT` to additionally accept
  noise-XK on a direct TCP port. Same identity, same whitelist.
- **TCP-only** (`--no-dmsg`): skip the dmsg client + listener
  entirely. Use when DMSG is unavailable or undesired
  (emergency-recovery, air-gapped); pair with `--tcplisten` and
  `--sk-from-visor` to inherit the host visor's PK.

## Config

`dmsgpty-host` is config-driven. Scaffold one:

```bash
./skywire dmsg pty host confgen > ./dmsgpty-host-config.json
```

Key fields in the JSON:

```json
{
  "sk": "<32-byte hex>",
  "dmsgdisc": "dmsg://022e607e...:80",
  "dmsgsessions": 1,
  "dmsgport": 22,
  "wl": ["<peer-pk-1>", "<peer-pk-2>"],
  "cliaddr": "/tmp/dmsgpty.sock",
  "clinet": "unix"
}
```

- `wl` is the dmsgpty whitelist — empty rejects all incoming pty
  requests. The host's own PK (derived from `sk`) is also
  implicitly allowed.
- `cliaddr`/`clinet` is the local control socket used by `skywire
  dmsg pty cli` to drive the host (start/exec from the same host).

Most flags can also be passed on the CLI; `--confstdin` reads the
config from stdin.

## DMSG + TCP listener

```bash
./skywire dmsg pty host \
  -c ./dmsgpty-host-config.json \
  --dmsgport 22 \
  --tcplisten :2022
```

- `--dmsgport` — dmsg-side port (peers dial `dmsg pty exec
  <host-pk>` and reach this).
- `--tcplisten :2022` — TCP-direct entry point. noise-XK gated by
  the same whitelist as the dmsg side. Mirrors the visor-embedded
  `dmsgpty.SshListen` field. Exposed CLI-side as
  `skywire cli sshd`.

### Reaching the TCP listener from outside the LAN

`--tcplisten :2022` binds on all interfaces. To accept connections
from the public internet, port-forward to the host:

- **Home / consumer router**: forward external TCP 2022 →
  `<host-lan-ip>:2022`. Allow inbound on the router firewall if
  required.
- **VPS / cloud host**: open the port in the provider's security
  group / cloud firewall. Also open the host firewall
  (`ufw allow 2022/tcp`, `firewall-cmd --add-port=2022/tcp
  --permanent`, etc.).
- **Mesh network (Tailscale, WireGuard, ZeroTier)**: bind to the
  mesh interface — `--tcplisten 100.x.y.z:2022` — and avoid
  exposing the port publicly. The mesh handles connectivity.
- **systemd socket-activation**: drop the `--tcplisten` flag and
  use `--listen-fd N` instead. systemd accepts the connection on
  a unit-defined `.socket` and hands the FD to a per-connection
  `dmsgpty-host@.service`. Lets you front the listener with
  TLS-terminating proxies or service-meshes that hand off raw FDs.

Peers connect from another host with:

```bash
./skywire cli sshd \
  --via tcp://<host-pk>@<host-public-ip>:2022 \
  -c /peer/skywire-config.json
```

Or send a one-shot pty exec:

```bash
./skywire cli dmsg pty exec \
  --via tcp://<host-pk>@<host-public-ip>:2022 \
  -c /peer/skywire-config.json \
  -- hostname
```

## TCP-only mode (no DMSG)

For air-gapped or DMSG-unreachable hosts:

```bash
./skywire dmsg pty host \
  -c ./dmsgpty-host-config.json \
  --no-dmsg \
  --tcplisten :2022 \
  --sk-from-visor /opt/skywire/skywire.json
```

- `--no-dmsg` skips the dmsg.Client + dmsg listener. The host runs
  as a TCP-only daemon.
- `--sk-from-visor <skywire-config.json>` loads the SK from a
  visor's `.sk` field. Lets the dmsgpty-host share the visor's PK
  identity so the whitelist on the peer's visor (which trusts the
  visor's PK) also applies to direct pty access.
- `--listen-fd N` is an alternative to `--tcplisten` for
  socket-activation deployments — one connection per process,
  exits on session end.

## Verifying it's listening

```bash
# Local — confirm the cli socket is up
ls -la /tmp/dmsgpty.sock

# From a peer host — confirm TCP is reachable
nc -zv <host-public-ip> 2022

# Full round-trip — open a session
./skywire cli sshd \
  --via tcp://<host-pk>@<host-public-ip>:2022 \
  -c /peer/skywire-config.json
# (interactive shell on the dmsgpty-host's host)
```

## See also

- [`skywire dmsg pty host`](../skywire/dmsg/pty/host/README.md) —
  auto-generated flag reference
- [Standalone skychat](standalone-skychat.md) — analogous
  detached-from-visor pattern for the chat-app
