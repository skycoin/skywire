# SOCKS5 proxy — client (skysocks-client)

`skysocks-client` is the client half of the [SOCKS5 proxy](README.md). It opens
a Skywire connection to a remote [`skysocks` server](README.md) and presents a
**local SOCKS5 listener** (default `127.0.0.1:1080`). Any SOCKS5-aware
application pointed at that local port has its traffic carried over Skywire and
exits to the internet from the **server visor's** location — so only the apps you
configure are proxied, and your exit IP becomes the server's.

It is controlled with `skywire cli proxy`.

## Quick start

```bash
skywire cli proxy test                       # find working servers (latency-ranked)
skywire cli proxy start --pk <server-pk>      # connect; local SOCKS5 on :1080
curl -x socks5h://127.0.0.1:1080 https://api.ipify.org   # verify exit IP
skywire cli proxy stop                        # disconnect
```

## Finding and testing servers

```bash
skywire cli proxy list      # public keys of visors running a skysocks server (from service discovery)
skywire cli proxy test      # test each: has-transport? + an HTTP request through it; shows reachability + latency
```

`proxy list` prints `<public-key> <country> <version>` per server. `proxy test`
ranks them by response latency; useful flags:

- `--connect` — just open transports to all online proxies (no HTTP test).
- `--version <v>` — only test servers on a given visor version.

## Starting the client

```bash
skywire cli proxy start --pk <server-pk> [flags]
```

Key flags (`skywire cli proxy start --help` for the full list):

| Flag | Purpose |
|---|---|
| `-k, --pk` | server public key (required) |
| `-n, --name` | name this client instance (lets you run several at once) |
| `-a, --addr` | local SOCKS5 listen address (default `:1080`) |
| `--http` | also expose an **HTTP** proxy at this address (in addition to SOCKS5) |
| `--mux` | parallel mux routes: `1`=single (default), `2+`=N routes, `0`=every distinct path (see [Multiplexed routes](#multiplexed-routes)) |
| `--mux-mode` | mux scheduler weighting: `auto` (latency-based, default) or `equal` (round-robin) |
| `--min-hops` | minimum routing hops for this session (`1`=no minimum; `>=2` forces traffic through intermediates) |
| `--reconnect` | keep re-dialing with backoff on route-group collapse instead of dropping the listener (default `true`) |
| `--existing-tp` | only use existing transports, don't create new ones |
| `--routing-policy` | per-app routing policy (`@/path/to/policy.star`) |
| `-t, --timeout` | start timeout (seconds) |
| `-v, --verbose` | stream the visor's logs scoped to this session (router/mux/setup events) until ctrl-c |

## Using the proxy

Point any SOCKS5 application at the client's local port. Use `socks5h://` (the
`h`) so DNS is resolved at the exit, not locally:

```bash
curl -x socks5h://127.0.0.1:1080 https://api.ipify.org
```

**Browser:** set the SOCKS5 host to `127.0.0.1` port `1080` and enable "proxy DNS
when using SOCKS v5" / "remote DNS".

**System-wide (one app at a time):** many tools honor `ALL_PROXY` /
`HTTP(S)_PROXY`:

```bash
ALL_PROXY=socks5h://127.0.0.1:1080 <command>
```

If you started with `--http <addr>`, that address is an ordinary HTTP/HTTPS proxy
for clients that don't speak SOCKS5.

## Multiplexed routes

A single proxy session can spread its traffic across **several parallel Skywire
routes** ("mux legs") for throughput and resilience, and adapt them at runtime as
network conditions change.

- Start with N legs: `--mux 2` (or more); `--mux 0` uses every distinct path.
- Weighting: `--mux-mode auto` favors lower-latency legs; `equal` is round-robin.
- Force legs through intermediates (e.g. for bandwidth-spreading): add
  `--min-hops 2`.

Manage the legs of a **running** session with the `mux-*` subcommands:

| Command | Effect |
|---|---|
| `proxy mux-info [--watch]` | per-leg traffic / latency (live with `--watch`) |
| `proxy mux-auto <preset>` | adaptive control loop (see below) |
| `proxy mux-set <n>` | reconcile to a target leg count |
| `proxy mux-add` / `mux-rm` | add / remove one leg |
| `proxy mux-mode auto\|equal` | change weighting at runtime |

`proxy mux-auto` runs an adaptive **prune-and-grow** loop toward a preset, both
shedding slow legs and re-growing fresh disjoint ones (honoring `--min-hops`)
when live legs drop — failover in both directions:

- `fastest` — primary + 1 lowest-latency leg
- `balanced` — primary + 3 lowest-latency legs
- `resilient` — up to 8 lowest-latency legs (max redundancy)

Pair it with `proxy mux-info --watch` in another terminal to see the effect.

## Reconnect behavior

With `--reconnect` (the default), a route-group collapse doesn't tear down the
SOCKS5 listener — the client keeps re-dialing the server with backoff, so an app
that connects during a server restart keeps its local socket and resumes when the
route comes back. `--reconnect=false` restores exit-on-failure.

## Auto-start config

`skysocks-client` ships in a generated config (visor routing `port: 13`,
`auto_start: false`). To start it automatically against a fixed server, set the
server key and flip `auto_start` (note: the **app arg is `-srv`**, while the CLI
flag is `--pk`):

```json
{
  "name": "skysocks-client",
  "args": ["-srv", "<server-public-key>", "--mux", "2"],
  "auto_start": true,
  "port": 13
}
```

## Controlling a remote visor

`--via dmsg://<pk>` (or `skynet://<pk>`) runs any `cli proxy` subcommand against a
**remote** visor instead of the local one — e.g. start/stop a proxy session on
another node you operate.

## Chaining with the resolving proxy

The [`.dmsg`/`.skynet` resolving proxy](../guides/resolving-proxy.md) can use a
skysocks client as its upstream: `skywire cli resolver up --upstream 127.0.0.1:1080`.
Then one browser proxy entry resolves `.dmsg`/`.skynet` over Skywire **and**
sends everything else out through the skysocks exit.

## Troubleshooting

- **`proxy start` times out / no route** — confirm the server is reachable:
  `proxy test` (or `proxy test --connect` to force transports). A server with no
  transport and no route won't connect.
- **Works then drops** — that's what `--reconnect` covers; check `proxy status`
  and `proxy mux-info` for leg health, and `--verbose` to watch the session live.
- **Slow** — add legs (`--mux 2+`) and/or `proxy mux-auto balanced`.

## See also

- [SOCKS5 proxy server](README.md) — the exit side (`skywire cli proxy server`)
- [command reference: `skywire cli proxy`](../skywire/cli/proxy/README.md)
- [resolving-proxy.md](../guides/resolving-proxy.md) — reach services by public key over dmsg/skynet
- [guides/socks5.md](../guides/socks5.md) — proxy vs VPN, walkthrough
