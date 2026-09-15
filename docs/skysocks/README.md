# SOCKS5 proxy — server (skysocks)

`skysocks` implements a SOCKS5 proxy over the Skywire network. A visor runs
the **server**; another visor runs the **[client](client.md)**, which exposes
a local SOCKS5 port that any conventional SOCKS5 application can use. All
traffic between the two visors is carried over an encrypted Skywire
transport.

The server optionally restricts who may connect to a list of public keys. With
no list set it accepts any authenticated peer — every transport is already
authenticated by key, so there is no anonymous case to guard.

## Usage

The server is controlled via `skywire cli proxy server`.

```bash
# Start the SOCKS5 server
skywire cli proxy server start

# Status
skywire cli proxy server status

# Stop
skywire cli proxy server stop
```

## Configuration

`skysocks` is enabled by default in a generated config (port `3`,
`auto_start: true`). To restrict it to named peers, pass `--whitelist` in
`args` — or start it with `skywire cli proxy server start -w <pk>,<pk>`:

```json
{
  "name": "skysocks",
  "args": ["--whitelist", "<pk>,<pk>"],
  "auto_start": true,
  "port": 3
}
```

Leave `args` empty to accept any peer:

```json
{
  "name": "skysocks",
  "args": [],
  "auto_start": true,
  "port": 3
}
```

## Connecting

Point a [SOCKS5 client](client.md) on another visor at this server's public
key. Once the client is running, a local SOCKS5 proxy is available — e.g.
verify it with `curl`:

```bash
curl -v -x socks5://localhost:1080 https://api.ipify.org
```

The local SOCKS5 port takes no credentials: access is decided at the server by
public key, not by a user:pass the client sends.

## See also

- [SOCKS5 proxy client](client.md)
- [VPN](../vpn/README.md) — full IP-level tunnelling (vs. per-application
  SOCKS5)
- [Command reference: `skywire cli proxy`](../skywire/README.md)
