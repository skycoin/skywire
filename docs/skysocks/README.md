# SOCKS5 proxy — server (skysocks)

`skysocks` implements a SOCKS5 proxy over the Skywire network. A visor runs
the **server**; another visor runs the **[client](client.md)**, which exposes
a local SOCKS5 port that any conventional SOCKS5 application can use. All
traffic between the two visors is carried over an encrypted Skywire
transport.

The server optionally requires a passcode (set in its configuration). If no
passcode is set, the server accepts connections without authentication.

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
`auto_start: true`). To require a passcode, pass `-passcode` in `args`:

```json
{
  "name": "skysocks",
  "args": ["-passcode", "123456"],
  "auto_start": true,
  "port": 3
}
```

Leave `args` empty for an open (no-auth) server:

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
curl -v -x socks5://123456:@localhost:1080 https://api.ipify.org
```

(omit the `123456:@` user:pass segment if the server has no passcode).

## See also

- [SOCKS5 proxy client](client.md)
- [VPN](../vpn/README.md) — full IP-level tunnelling (vs. per-application
  SOCKS5)
- [Command reference: `skywire cli proxy`](../skywire/README.md)
