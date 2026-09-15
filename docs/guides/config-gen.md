# Visor configuration — config generation

How the visor's JSON config is produced, and the SKYENV variables that drive
it.

**On a packaged install, you want `skywire autoconfig`, not `config gen`
directly** — every package update regenerates the config from
`/etc/skywire.conf`, so a hand-run `config gen` is replaced at the next
update. See [which one to use](configuration.md#which-one-autoconfig-or-config-gen).
This page documents what both of them are driving.

For runtime configuration — changing a running visor without editing the
config — see [config-runtime.md](config-runtime.md).

## The two things to run

This page does not reproduce the command's help or the SKYENV template,
because the binary prints both and a pasted copy goes stale (it had, in three
separate places, each differently):

```bash
skywire cli config gen --all      # every flag, including the hidden ones
skywire cli config gen -q         # the annotated SKYENV template
```

`-q` is the one to start from for a system install:

```bash
skywire cli config gen -q | sudo tee /etc/skywire.conf   # write the template
# edit /etc/skywire.conf, uncommenting what you want
skywire autoconfig                                        # apply it
```

The per-flag reference is generated from the live command tree and served
alongside this page at [cli/config](../skywire/cli/config/README.md).

## SKYENV variables

Every variable the template carries, with its default. **Generated** from
`skywire cli config gen -q` — the template is the authority, and the table it
replaced was hand-maintained, had drifted to 44 of these 99, and still listed
one that no longer exists. Regenerate rather than edit by hand.

### Installation path

| Variable | Default | Description |
|----------|---------|-------------|
| `PKGENV` | `true` | Default config paths for the installer or package (system paths) |
| `USRENV` | `true` | Default config paths for the current userspace |
| `SVCCONF` | `"services-config.json"` | service conf path override |
| `DMSGCONF` | `"dmsghttp-config.json"` | dmsghttp config path override |
| `OUTPUT` | `'./skywire-config.json'` | Output path of the config file |
| `BINPATH` | `'./apps'` | Set app bin_path |

### Deployment

| Variable | Default | Description |
|----------|---------|-------------|
| `SVCCONFADDR` | `('')` | Set custom service conf URLs |
| `TESTENV` | `true` | Use test deployment |
| `DMSGHTTP` | `true` | Use dmsghttp to connect to the production deployment |
| `MINDMSGSESS` | `8` | Number of dmsg serverts to connect to (0 unlimits) |

### Transports

| Variable | Default | Description |
|----------|---------|-------------|
| `VISORISPUBLIC` | `true` | Other Visors will automatically establish transports to this visor requires port forwarding or public ip |
| `DISABLEPUBLICAUTOCONN` | `true` | Disable auto-transports to public visors from this visor |
| `TPSETUPPKS` | `('')` | Add transport setup public keys |

### Ports

| Variable | Default | Description |
|----------|---------|-------------|
| `SUDPHPORT` | `0` |  |
| `STCPRPORT` | `0` |  |
| `TRANSPORTPORT` | `0` |  |

### Routing

| Variable | Default | Description |
|----------|---------|-------------|
| `ROUTESETUPPKS` | `('')` | Add route setup-node public keys |
| `CALCULATEROUTES` | `true` | Enable local route calculation (instead of using route finder) |

### Remote Access

| Variable | Default | Description |
|----------|---------|-------------|
| `HYPERVISORPKS` | `('')` | Set remote hypervisor public keys |
| `WSPEERS` | `('')` | Peers held as WebSocket transports at a known address: <pk>@<ws(s)://host[:port]/path> |
| `DMSGPTYPKS` | `('')` | Grant access to pseudoterminal (pty) for public keys |

### Survey Access

| Variable | Default | Description |
|----------|---------|-------------|
| `SURVEYPKS` | `('')` | Grant access for survey collection to these public keys |

### Hypervisor UI

| Variable | Default | Description |
|----------|---------|-------------|
| `ISHYPERVISOR` | `true` | Start the hypervisor interface for this visor |
| `LEGACYHVUI` | `true` | Serve the legacy Angular dashboard at the hypervisor web UI root instead of the desk (no wasm visor in the page). Default false = the desk. Runtime: skywire cli visor hv enable --legacy[=false] -w |
| `HVHTTPADDR` | `':8000'` | Hypervisor web-UI listen address (host:port). Default ':8000' = all interfaces (reachable on the LAN at http://<this-host-ip>:8000). Use '127.0.0.1:8000' to restrict the UI to localhost, or pin a specific LAN IP, e.g. '192.168.0.2:8000'. |
| `ENABLEPKENDPOINT` | `true` | Expose an unauthenticated GET /api/pk on the hypervisor (returns this visor's public key). Off by default; skybian / Arch-ARM image builds set it so a freshly-imaged board can be discovered on the LAN. |

### Browse origin

| Variable | Default | Description |
|----------|---------|-------------|
| `BROWSESUFFIX` | `'.example.net'` | Domain the "real-origin" browse path serves untrusted mesh content under — a SEPARATE eTLD+1 from the visor's own domain, so browsed content is cookie- and origin-isolated from the visor identity. Leading dot. Empty uses the deployment default from services-config.json, and on a local visor that is '.mesh.localhost' (loopback, a secure context, no certificate needed). |
| `BROWSETLSCERT` | `'/etc/skywire/browse.crt'` | A real wildcard certificate for *.<BROWSESUFFIX>, so the browse origin and the status pages load over HTTPS with no warning. Both are needed; empty means plain HTTP on loopback. |
| `BROWSETLSKEY` | `'/etc/skywire/browse.key'` |  |
| `HVAUTH` | `false` | Password gate on the hypervisor UI. Unset leaves it as-is: on for a first run, and a regen keeps whatever the config already had. Set it to make the choice survive a config rebuild (autoconfig runs on every package update). Windows and macOS force the gate on regardless. |
| `LANDMSGPORT` | `8082` | Pin the DMSG server's TCP port for stable WAN reachability. Default 0 = OS-assigned at runtime (changes every restart — fine for LAN-only, bad for remote visors that need a stable address). Set to a chosen port (e.g. 8082) and port-forward it on your router for WAN access. |
| `LANDMSGPUBLIC` | `'203.0.113.42:8082'` | Advertise a WAN-reachable address to remote visors (host:port). Empty = LAN-only. Combine with LANDMSGPORT + a router port-forward for hypervisors on a NAT. |
| `DMSGSERVERCONF` | `'/etc/skywire-dmsg.json'` | Run a public dmsg server inside the visor from a standalone dmsg-server config file (the one "skywire dmsg server start <file>" used). Replaces a separate dmsg server unit on the same host: stop and disable that unit first, and drop it from RESTART_SERVICES. The server keeps its own key, ports, wss domain and health endpoint from that file. |
| `DMSGSERVER` | `true` | Run a dmsg server inside the visor on the visor's OWN key, so the host has one identity and one discovery entry carrying both roles. The server shares the visor's transport port, so pin TRANSPORTPORT to a port that is actually reachable from outside. Mutually exclusive with DMSGSERVERCONF, which runs a server on its own key instead. |
| `DMSGSERVERPUBLIC` | `'1.2.3.4:30084'` | Address the in-visor dmsg server advertises (host:port). Empty advertises whatever its listener resolves to, which is only right on a LAN. |
| `DMSGSERVERWSTLS` | `':443'` | Where the in-visor dmsg server self-terminates TLS for its wss front via Let's Encrypt, so a browser or wasm visor can reach it. Set it on a host with NO reverse proxy — including any host where the standalone dmsg server used to be the thing serving :443, since folding it into the visor retired that listener. Leave it empty where Caddy (or another front) already owns :443. |

### Rewards

| Variable | Default | Description |
|----------|---------|-------------|
| `REWARDSKYADDR` | `''` | Skycoin reward address or xpub key |

### Apps

| Variable | Default | Description |
|----------|---------|-------------|
| `DISPLAYNODEIP` | `true` | Display the node ip in the service discovery for any public services this visor is running |
| `VPNSERVER` | `true` | Autostart vpn server for this visor |
| `VPNROUTER` | `false` | Autostart the vpn-router: a LAN/WiFi gateway that NATs downstream clients into the vpn-client tunnel. Needs VPNROUTERLANIFC (the downstream interface). Requires root + the vpn-client running. |
| `VPNROUTERLANIFC` | `'eth1'` | Downstream interface the vpn-router serves. Ethernet-out: a second/USB NIC (e.g. eth1). WiFi-out: the wireless interface (e.g. wlan0) — also set VPNROUTERWIFI=true. The visor's own uplink (that reaches the vpn-server over the mesh) must be a DIFFERENT interface. |
| `VPNROUTERSUBNET` | `'192.168.42.1/24'` | vpn-router gateway + downstream subnet, as <gateway-ip>/<prefix> (default 192.168.42.1/24) |
| `VPNROUTERWIFI` | `false` | WiFi-out: run hostapd (an access point) on VPNROUTERLANIFC so wireless clients associate and are routed through the VPN. Leave false for the ethernet-out variant. (rtl8723bs on the original skyminer boards can be unstable in AP mode — a USB WiFi dongle is the robust option.) |
| `VPNROUTERSSID` | `'skywire-vpn'` | WiFi SSID / WPA2 passphrase (8–63 chars) for the WiFi-out variant. Set VPNROUTEROPEN=true for a passphrase-less open network instead. |
| `VPNROUTERPASSPHRASE` | `''` |  |
| `VPNROUTEROPEN` | `false` |  |
| `VPNROUTERBAND` | `'2.4'` | WiFi band ('2.4' or '5'), channel (0 = default for the band), and regulatory country code for the WiFi-out variant. |
| `VPNROUTERCHANNEL` | `0` |  |
| `VPNROUTERCOUNTRY` | `'US'` |  |
| `VPNROUTERMESHGW` | `false` | Mesh gateway: additionally let downstream clients reach mesh services by name — resolve *.dmsg / *.skynet to a synthetic IP and transparently proxy the connection over the mesh (no SOCKS, no per-device setup). The dest port is the mesh routing port. VPNROUTERMESHGWCIDR is the synthetic-IP pool (default 100.64.0.0/16; change only if it collides with your LAN). |
| `VPNROUTERMESHGWCIDR` | `'100.64.0.0/16'` |  |
| `VPNROUTERMESHTLS` | `false` | Mesh gateway HTTPS: TLS-MITM connections to *.dmsg / *.skynet on :443 — the gateway terminates TLS with a self-generated CA (persisted under <local>/mesh-gateway-ca/) and bridges plaintext to the mesh service, so browsers get a secure context. LAN clients must install that CA as trusted; its path + fingerprint are logged on first start. |
| `PROXYCLIENTPK` | `''` | Set server public key for proxy client to connect to |
| `STARTPROXYCLIENT` | `true` | Enable autostart of the proxy client |
| `PROXYSERVER` | `false` | Autostart proxy server |
| `SKYCHAT` | `false` | Autostart skychat |
| `SKYCHATADDR` | `'127.0.0.1:8001'` | Skychat local address |
| `SKYCHATPORTLESS` | `false` | through the hypervisor. Default: false (skychat binds SKYCHATADDR). |
| `SKYCHATPAIR` | `false` | Default: true. Set to false to disable group-chat plumbing. |
| `DMSGWEB` | `true` | Autostart the dmsgweb SOCKS bridge (browse dmsg sites over a local SOCKS5 proxy). Off by default. |
| `DMSGWEBUPSTREAM` | `'127.0.0.1:4446'` | makes one proxy cover .dmsg, .skynet and clearnet. |
| `SKYNETWEB` | `true` | Autostart the skynetweb bridge (serve/reach skynet sites). Off by default. |
| `SKYNETWEBUPSTREAM` | `'127.0.0.1:1080'` | 127.0.0.1:1080 is the skysocks-client, i.e. the clearnet exit. |
| `DMSGWEBADDR` | `'0.0.0.0'` |  |
| `SKYNETWEBADDR` | `'0.0.0.0'` |  |
| `RESOLVERS` | `('')` | A .dmsg and a .skynet proxy side by side: RESOLVERS=('dmsg:4447;name=alt' 'skynet:4448;name=alt-skynet') |
| `SKYMAILBRIDGE` | `false` | Autostart the skymail bridge (SMTP <-> skywire mail gateway). Off by default. |
| `PROXYSERVERWL` | `('')` | Whitelist public keys for the proxy server (empty = allow all) |
| `VPNKS` | `true` | Set VPN client killswitch |
| `ADDVPNPK` | `''` | Set vpn server public key for the vpn client to use |
| `VPNSERVERWL` | `('')` | Whitelist public keys for the vpn server (empty = allow all) |
| `VPNSEVERSECURE` | `''` | Change secure mode status of vpn server |
| `VPNSEVERNETIFC` | `''` | Set VPN Server network interface - i.e. eth0 |

### Skycoin embedded apps

| Variable | Default | Description |
|----------|---------|-------------|
| `SKYCOIND` | `true` | Autostart skycoin daemon (full node, syncs the chain) |
| `SKYCOIND_FIBER_TOML` | `'/path/to/fiber.toml'` | FIBER_TOML path. Empty = vanilla skycoin. Set to a fiber.toml to make the daemon serve a fibercoin chain instead. |
| `SKYCOIND_API_SETS` | `'STATUS,WALLET,READ'` | GUI API sets to enable on the daemon (comma-separated list). Empty = the daemon's compiled-in defaults. See: 'skywire skycoin daemon --help' for the full list. Common values: STATUS,WALLET,READ,TXN,BACKGROUND_SCANNER |
| `SKYCOIND_USER` | `'youruser'` | Drop skycoin daemon to this user (POSIX setuid before exec). Empty = run as the visor's own UID. The chain ends up under ~<user>/.skycoin/data.db, so picking the right user matters for chain ownership across upgrades. |
| `SKYCOIND_INSTANCES` | `('skycoin' 'mdl')` | Run multiple skycoin daemon instances (one per fibercoin chain), comma-separated instance names. Empty = the single default daemon. Each instance takes its own fiber.toml / API-sets / port set. |
| `SKYCOIND_FLAGS` | `'-launch-browser=false'` | Extra flags passed verbatim to each skycoin daemon invocation. |
| `SKYCOINWEB` | `true` | Autostart skycoin-web thin-client wallet |
| `SKYCOINWEBADDR` | `'127.0.0.1:8002'` | skycoin-web bind address (default 127.0.0.1:8002 — bumped one up from skycoin's upstream default of 8001 because skychat is already pinned at 127.0.0.1:8001 in skywire). |
| `SKYCOINWEBNODES` | `('')` | Node URLs the wallet talks to. Bash array — set one per fibercoin you want the wallet to multi-coin-browse. Empty = upstream default (https://node.skycoin.com). For local-daemon use, point at the daemon's bind: SKYCOINWEBNODES=('http://127.0.0.1:6420') For multi-chain: SKYCOINWEBNODES=('http://127.0.0.1:6420' 'http://127.0.0.1:6421') |
| `SKYCOINWEBWALLET` | `''` | Wallet directory override. Empty = upstream default at $HOME/.skycoin/wallets of whichever user the app runs as (see SKYCOINWEBUSER below). |
| `SKYCOINWEBUSER` | `'youruser'` | Drop skycoin-web to this user (POSIX setuid before exec). Empty = run as the visor's own UID. Required when the visor runs as _skywire and the wallet should access the operator's ~/.skycoin/wallets directory. The launcher only honors this in external mode (Binary set on the AppConfig entry, which the default skycoin-web entry already has). |
| `COIN_NODES` | `('')` | Advertise already-running fibercoin nodes over dmsg + service discovery (type=coin), so a thin-client wallet — including the browser wasm visor — can discover and reach a node for the right coin over the mesh, with no local full node and no launch flags. Bash array. Each entry is "<local_addr>[@<dmsg_port>]" where local_addr is the node's HTTP API on this host; dmsg_port defaults to local_addr's port. The node runs INDEPENDENTLY of the visor (it need not be a skywire SKYCOIND app) and is health-gated: the visor advertises it only while its /api/v1/health answers. COIN_NODES=('127.0.0.1:6420') Multi-coin (skycoin + a fibercoin on 6430): COIN_NODES=('127.0.0.1:6420' '127.0.0.1:6430@6430') |

### Advanced Tuning

| Variable | Default | Description |
|----------|---------|-------------|
| `CLIADDR` | `'localhost:3435'` | CLI RPC address (default localhost:3435) Use 0.0.0.0:3435 for Docker/remote access |
| `STUNSERVERS` | `('')` | STUN servers for NAT traversal |
| `SHUTDOWNTIMEOUT` | `'10s'` | Graceful shutdown timeout (default 10s) |
| `REGTIMEOUT` | `'10m'` | Public visor registration timeout (default 10m) |
| `MAXTRANSPORTS` | `2048` | Public visor max transports (default 1000) |
| `MUXROUTES` | `0` | Number of parallel mux routes per connection (default 0) |
| `POLICYPERDIAL` | `''` | Per-dial routing policy: preset:<name> (e.g. preset:adaptive), @/path/policy.star, @/path/policy.wasm, inline Starlark, or empty |

### Auto-Update (skywire-autoupdate package)

| Variable | Default | Description |
|----------|---------|-------------|
| `UPDATE_CHANNEL` | `develop` | Update channel: "develop"        = latest develop branch commit (default) "latest"         = latest tagged release version "<hash>"         = pin to a specific commit hash "binary"         = download the prebuilt linux binary for this arch from the rolling <branch>-latest GitHub pre-release, verified against SHA256SUMS — no compile, no Go toolchain. Recommended for unattended hosts (avoids all source-build/network fragility). "binary-develop" / "binary-master" = force the develop / master prebuilt binary |
| `GOPROXY_MODE` | — | Go module proxy mode for the source-build channels (develop/latest/<hash>). Unset (default): try GOPROXY=direct first, then fall back to the default module proxy (proxy.golang.org) if the direct git fetch fails. "direct" = only ever fetch direct from git (no proxy fallback) "proxy"  = only ever use the default module proxy (never direct) |
| `DEPLOY_DIR` | `''` | Docker deployment directory (for skywire-docker-update) Set this to enable auto-updating docker-based deployment services. The directory must contain a compose.yaml or docker-compose.yml. |
| `RESTART_SERVICES` | `('dmsgweb-surveys.service')` | Extra services to restart after a successful binary update. Bash array of systemd unit names, restarted at the END of skywire-update — ONLY when a new binary was actually installed (never on a no-op update tick or a plain 'skywire autoconfig' run). Use for standalone units that depend on the skywire binary, e.g. a separate dmsgweb SOCKS5 proxy. The skywire service is always restarted on update; do not list it here. |

### Miscellaneous

| Variable | Default | Description |
|----------|---------|-------------|
| `SK` | `''` | Set secret key |
| `VERSION` | `''` | Custom config version override |
| `LOGLVL` | `debug` | Set visor runtime log level. Default is info ; uncomment for debug logging |

## JSON Config Sections

The generated `skywire-config.json` contains these top-level sections:

| Section | Description | Runtime equivalent |
|---------|-------------|--------------------|
| `dmsg` | DMSG client configuration | [DMSG management](config-runtime.md#dmsg-dmsg) |
| `dmsgpty` | Pseudoterminal access | — |
| `transport` | Transport layer config | [Transport management](config-runtime.md#transport-transport) |
| `routing` | Route setup nodes, route finder | [Route management](config-runtime.md#routing-routing) |
| `launcher` | App launcher, service discovery | [App management](config-runtime.md#apps-launcher) |
| `rewards` | Reward system UI config | — |
| `hypervisor` | Hypervisor web UI | [Hypervisor](config-runtime.md#hypervisor-hypervisor) |

## Config Update (persistent)

Update the config file without regenerating (changes apply on restart):

```bash
skywire cli config update -a                          # refresh service endpoints
skywire cli config update hv --add-pks <pk>           # add hypervisor
skywire cli config update ss --whitelist <pk1>,<pk2>  # proxy server whitelist
skywire cli config update vpns --netifc eth0          # VPN server interface
skywire cli config update vpnc --add-server <pk>      # VPN client server
skywire cli config update sc --add-server <pk>        # proxy client server
skywire cli config update --log-level debug           # log level
skywire cli config update --public-autoconn true      # public autoconnect
skywire cli config update --set-minhop 1              # minimum route hops
```

For runtime changes that take effect immediately (without restart),
see [config-runtime.md](config-runtime.md).
