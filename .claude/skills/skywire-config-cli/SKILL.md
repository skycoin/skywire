---
name: skywire-config-cli
description: >-
  The `skywire cli config` command group, generate, inspect, parse, and update
  a skywire visor config. Covers `config gen` and its privacy/service knobs
  (min-hops, ar-transport-limit, no-direct-transports, mux-fec,
  policy_per_dial, cascade, pty-rpc-exec), `config update` and its app
  subcommands (hv/sc/ss/vpnc/vpns/svc), plus gen-keys/pk/check-pk/parse/show.
  Load when generating or editing a visor config or documenting its knobs.
---

# skywire cli config

Source: `cmd/skywire-cli/commands/config/` (package `cliconfig`). Surface verified
against the built CLI. `config gen` and `config update` are **destructive to a
live config**, always test against a temp path (`-o /tmp/x.json`), never a
running visor's `skywire-config.json` or `/opt/skywire/`.

## Subcommand map

| Command | Purpose | Mutates a config? |
|---|---|---|
| `config gen` | generate a new config file | YES — writes `-o` (default `./skywire-config.json`) |
| `config gen-keys` | print a fresh pk/sk keypair | no |
| `config pk <sk-hex>` | derive pk from a secret key | no |
| `config check-pk <pk>` | validate a skywire public key | no |
| `config parse <file>` | parse-check a config file, report version compat | no (read-only) |
| `config show [jq-filter]` | print the running visor's config (RPC, file fallback) | no (read-only) |
| `config update [flags]` | rewrite endpoints / log-level / autoconn / min-hops in place | YES |
| `config update hv` | add/reset hypervisor PKs | YES |
| `config update sc` | set/reset skysocks-client `--srv` | YES |
| `config update ss` | set/reset skysocks server `--whitelist` | YES |
| `config update vpnc` | set killswitch/server, reset vpn-client | YES |
| `config update vpns` | whitelist/secure/autostart/netifc, reset vpn-server | YES |
| `config update svc` | refresh local `services-config.json` from bootstrap | writes services file (default `/opt/skywire/services-config.json`) |

Global flags on all: `--json`, `--jq <expr>`, `--shape`, `--via dmsg://<pk>`.

## `config gen`, the big one

Most flags are **hidden by default**; `config gen --all` prints the full menu
and exits. `config gen -q` prints the `skywire.conf` (SKYENV) template reflecting
any flags passed; `-Q <file>` writes it. Every flag has a matching `${ENVVAR}`
read from the SKYENV file (`SKYENV=/path/skywire.conf`) or `/etc/skywire.conf`.

Output/target flags:
- `-o, --out <file>` output path (default `skywire-config.json`; `--out /dev/stdout` or `-n`)
- `-n, --stdout` write to stdout · `-N, --squash` no whitespace
- `-r, --regen` regenerate existing file, retain keys (preserves min_hops, cascade and policy_per_dial across regen)
- `-f, --force` delete pre-existing config first (mutually exclusive with `-r`)
- `-x, --retainhv` keep existing hypervisors on regen
- `-p, --pkg` package paths (`/opt/skywire`) · `-u, --user` `$HOME` paths (also implies `--ishv`)
- `-i, --ishv` local hypervisor · `-j, --hvpks <csv>` remote hypervisor PKs
- `-s, --sk <hex>` use a specific secret key (random if unset)

Privacy / routing / transport knobs (verified to land in the JSON):
- `--min-hops N` → `routing.min_hops`. **1** = allow direct 1-hop routes (default);
  **>=2** forces multihop through intermediaries for sender privacy.
- `--ar-transport-limit N` → `transport.ar_transport_limit`. **0** = stay
  registered with the address-resolver; **N>0** = deregister after N transports;
  **N<0** = never register (inbound-invisible).
- `--no-direct-transports` → `transport.no_direct_transports`. Never create direct
  p2p transports; dmsg (relay) is still allowed.
- `--transport-port N` → `transport.transport_port`. Shared master port carrying
  every transport type (stcpr+WS on tcp, quic+sudph+wt+webrtc on udp). `--stcpr`
  / `--sudph` set per-type ports that override it.
- `--mux-fec` turns on forward error correction on mux routes. The route count is a routing policy output now, so there is no flag for it.
- `--policy <spec>` → `routing.policy_per_dial`. Per-dial routing policy:
  `preset:<name>` (e.g. `preset:adaptive`), `@/path/policy.star`,
  `@/path/policy.wasm`, inline Starlark, or empty for built-in defaults.
  The default is empty, so the adaptive preset is opt-in.
- `--cascade` → `routing.enable_cascade_route_setup`. Opt into source-driven
  cascade route setup; default is the legacy setup-node path.
- `--calculate-routes` → `routing.calculate_routes` (local route calc, no RF).
- `--pty-rpc-exec` → allow visor-RPC-initiated dmsgpty exec (control/jump-node
  opt-in; **off closes a local privilege-escalation vector**).

Deployment / service knobs:
- `-a, --url <url>` services-conf URL · `-d, --dmsghttp` dmsg-only (this is the
  default; kept as a hidden no-op) · `--nofetch` skip fetching services (use with
  embedded/`SKYDEPLOY` defaults) · `--nodefaults` no hardcoded service defaults
  · `--minsess N` dmsg servers to connect (0 = unlimited).
- `-z, --public` publicize visor in SD · `-y, --autoconn` disable public
  autoconnect · `--maxtransports`, `--regtimeout` public-visor tuning.
- Service-node config generators (hidden, one per service): `--sn --dmsgdisc
  --dmsgsrv --tpd --sd --ar --rf`.
- Whitelists: `--dmsgpty <csv>`, `--survey <csv>`, `--routesetup <csv>`,
  `--tpsetup <csv>`.
- Hypervisor UI: `-c/--noauth`, `-e/--auth`, `--pk-endpoint` (unauth GET /api/pk).
- Embedded LAN/WAN DMSG server (on whenever `--ishv`): `--lan-dmsg-port`,
  `--lan-dmsg-public`.

App autostart / config knobs (each has a `SERVE*`/env toggle):
- VPN: `-v/--servevpn` (default true), `--servevpnrouter` + the `--vpnrouter-*`
  family (lan-ifc, subnet, wifi, ssid, passphrase, band, channel, country, open,
  mesh-gateway[-cidr][-tls]).
- Proxy: `--serveproxy` (default true), `--proxywl`, `--proxyclientpk`,
  `--startproxyclient`.
- Skychat: `--servechat` (default true), `--chataddr`, `--servechatpair`
  (needed for group chat), `--chatportless`.
- Skycoin: `--skycoind*` (daemon, multi-instance), `--skycoinweb*` (web wallet),
  `--coin-nodes`, `--wallet-custody browser|disk|remote`, `--wallet-dir`,
  `--wallet-remote`, `--no-wallet`.
- Embedded resolving proxies: `--dmsgweb` (:4445), `--skynetweb` (:4446),
  `--skymail-bridge` (:1025), plus `--*-upstream` / `--*-addr`.
- Visor runtime: `--memory-limit` (a size, `auto` or `none`), `--autoconn-max-total`, `--autoconn-site-links`, `--autoconn-max-per-type sudph=40,webrtc=10`, `--stats-retention` (days), `--flight-recorder`, `--log-server-addr`, `--pty-ssh-listen`, `--pty-persist`, `--no-dmsgscp`, `--hv-ui-disable`, `--hvdeskaddr`.
- Whole deployment in one visor: `--deployment <host[:port]>` with `--deployment-redis` and `--deployment-wss-suffix`. See skywire-deployment-services.
- `--rewardaddr <skyaddr|xpub>`, `--example-apps`, `--external-apps`,
  `--disableapps <csv>`, `--binpath`.

## `config update`, in-place edits (target must already exist)

Select target with `-i/--input` (read) and `-o/--output` (write); one given =
both. `-p/--pkg` = package config, `-u/--user` = `$HOME` config. **checkConfig
fails fast if the output file does not exist**, so `config update` in a dir
without `skywire-config.json` is a no-op error, it will not clobber a config it
can't find. Top-level: `-a/--endpoints` (refetch service endpoints; passing
`-b/--url <url>` now implies this), `--log-level trace|debug|info`,
`--public-autoconn true|false`, `--set-minhop N`. Subcommands hv/sc/ss/vpnc/vpns
edit launcher app args; each has `-r/--reset`. `svc` refetches the two
services-config blobs via RPC→DMSG→HTTP (`--no-cxo/--no-rpc/--no-dmsg` skip a leg).

## Gotchas

- `config show --path` returns the path the running visor actually loaded (RPC);
  falls back to the default install path when no visor is reachable.
- `check-pk` validates the point is on the curve, not just hex length, a
  random 66-hex string usually fails.
- `gen` refuses to overwrite an existing file without `-r`/`-f`.
- As root, `gen` writing to a non-root-owned dir warns (except `-p`); a non-root
  user writing to a root-owned path is fatal.
