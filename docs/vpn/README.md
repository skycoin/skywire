# VPN — server (vpn-server)

`vpn-server` implements full IP-level VPN functionality over the Skywire
network. A visor runs the **server**; another visor runs the
**[client](client.md)**, which tunnels all of its machine's traffic through
the server. Unlike the per-application [SOCKS5 proxy](../skysocks/README.md),
the VPN captures traffic at the IP layer.

The server optionally restricts who may connect to a list of public keys. With
no list set it accepts any authenticated peer — every transport is already
authenticated by key, so there is no anonymous case to guard.

!!! note "Separate machines"

    Because of how the VPN reconfigures host networking, the VPN server and
    VPN client must run on **different machines** — you cannot run both on
    one host.

## Usage

The server is controlled via `skywire cli vpn server`.

```bash
# Start the VPN server
skywire cli vpn server start

# Status
skywire cli vpn server status

# Stop
skywire cli vpn server stop
```

## Configuration

`vpn-server` ships in a generated config (port `44`, `auto_start: true`).
To restrict it to named peers, pass `--whitelist` in `args` — or start it with
`skywire cli vpn server start -w <pk>,<pk>`:

```json
{
  "name": "vpn-server",
  "args": ["--whitelist", "<pk>,<pk>"],
  "auto_start": true,
  "port": 44
}
```

Leave `args` empty to accept any peer.

## See also

- [VPN client](client.md) — tunnel a machine's traffic through a VPN server.
- [VPN router](router.md) — turn a board into a WiFi/ethernet VPN gateway for
  the devices behind it.
- [SOCKS5 proxy](../skysocks/README.md) — per-application proxying as a
  lighter alternative.
- [Command reference: `skywire cli vpn`](../skywire/README.md)
