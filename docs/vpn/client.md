# VPN client (vpn-client)

`vpn-client` is the client half of the [Skywire VPN](README.md). It opens a
Skywire connection to a remote [VPN server](README.md) and tunnels **all** of the
machine's traffic through it — your public IP becomes the server's. Unlike the
[SOCKS5 proxy](../skysocks/client.md) (which proxies one app at a time), the VPN
captures the whole machine at the IP layer.

!!! note "Separate machines"

    The VPN client and VPN server must run on **different machines** (see the
    [server page](README.md)).

!!! warning "Elevated permissions"

    The client rewrites the system routing table and creates a TUN device, so it
    needs `CAP_NET_ADMIN` (run via sudo, or grant the capability). See
    [guides/permissions.md](../guides/permissions.md).

## Quick start

```bash
skywire cli vpn list                     # VPN servers from service discovery
skywire cli vpn start --pk <server-pk>   # connect; all traffic now tunneled
skywire cli vpn status
skywire cli vpn stop
```

## Subcommands

| Command | Purpose |
|---|---|
| `vpn list` | list visors running a VPN server (from service discovery) |
| `vpn start --pk <pk>` | connect to a server and bring the tunnel up |
| `vpn status` | show the current client session |
| `vpn stop` | tear the tunnel down |
| `vpn ui` / `vpn url` | open / print the VPN web UI |
| `vpn server` | control the local VPN **server** (see [server page](README.md)) |

## Starting the client

```bash
skywire cli vpn start --pk <server-pk> [flags]
```

| Flag | Purpose |
|---|---|
| `-k, --pk` | server public key (required) |
| `-t, --timeout` | start timeout (seconds) |
| `--existing-tp` | only use existing transports, don't create new ones |
| `--local-route` | calculate routes locally instead of using the route finder |
| `--routing-policy` | per-app routing policy (`@/path/to/policy.star`) |
| `-v, --verbose` | stream the visor's logs scoped to this session (router/mux/setup) until ctrl-c |
| `--via dmsg://<pk>` | run the command against a **remote** visor instead of the local one |

## Auto-start config

`vpn-client` ships in a generated config (visor routing `port: 43`,
`auto_start: false`). To connect automatically to a fixed server, set the server
key (and a passcode if the server requires one) and flip `auto_start` — note the
**app args use `-srv` / `-passcode`**, while the CLI flag is `--pk`:

```json5
{
  "name": "vpn-client",
  "args": [
    "-srv", "<server-public-key>",
    "-passcode", "1234"          // omit if the server has no passcode
  ],
  "auto_start": true,
  "port": 43
}
```

## Verifying

With the VPN up, your detected public IP should be the server's, and traffic
takes an extra hop:

```bash
curl https://api.ipify.org      # should show the server's IP
traceroute google.com
```

The web UI (`skywire cli vpn ui`) shows connection status, the server key, and
live throughput.

## Troubleshooting

- **Permission denied / no tunnel** — the client needs `CAP_NET_ADMIN`; run with
  sudo or grant the capability ([permissions guide](../guides/permissions.md)).
- **`start` times out** — the server may be offline or unreachable; pick another
  from `vpn list`. A server with no transport and no route won't connect.
- **No internet after connecting / DNS broken** — `vpn stop` restores the
  original routes; if a hard crash left stale routes, restart networking. Watch a
  live session with `--verbose`.
- **Same machine** — client and server cannot share a host; use two visors.

## See also

- [VPN server](README.md) — the exit side (`skywire cli vpn server`)
- [command reference: `skywire cli vpn`](../skywire/cli/vpn/README.md)
- [guides/vpn.md](../guides/vpn.md) — full VPN walkthrough
- [guides/permissions.md](../guides/permissions.md) — required capabilities
- [SOCKS5 proxy client](../skysocks/client.md) — per-app alternative to a full VPN
