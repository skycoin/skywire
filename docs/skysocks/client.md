# SOCKS5 proxy — client (skysocks-client)

`skysocks-client` is the client half of the [SOCKS5 proxy](README.md). It
opens a persistent Skywire connection to a configured remote
[`skysocks` server](README.md) and a local TCP port; all traffic arriving on
that local port is forwarded over the Skywire connection. Any conventional
SOCKS5 application can then use the local port.

## Usage

The client is controlled via `skywire cli proxy`.

```bash
# Start the client against a remote skysocks server
skywire cli proxy start --pk <server-public-key>

# List known proxy servers
skywire cli proxy list

# Status
skywire cli proxy status

# Stop
skywire cli proxy stop
```

By default the client listens on local SOCKS5 port `1080`.

## Configuration

`skysocks-client` ships in a generated config (port `13`,
`auto_start: false`). To start it automatically against a fixed server, set
`-srv` (and `-passcode` if the server requires one) and flip `auto_start`:

```json
{
  "name": "skysocks-client",
  "args": ["-srv", "024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7"],
  "auto_start": true,
  "port": 13
}
```

## Using the proxy

Point any SOCKS5-aware application at the client's local port:

```bash
curl -v -x socks5://localhost:1080 https://api.ipify.org
```

Your traffic exits to the internet from the remote server visor.

## Multiplexed routes

A proxy session can spread its traffic across several parallel routes for
throughput and resilience. The `skywire cli proxy mux-*` commands
(`mux-add`, `mux-rm`, `mux-set`, `mux-mode`, `mux-auto`, `mux-info`) manage
the mux legs of an active session at runtime.

## See also

- [SOCKS5 proxy server](README.md)
- [Command reference: `skywire cli proxy`](../skywire/README.md)
