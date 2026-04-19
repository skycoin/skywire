# Visor Configuration — Config Generation

This document covers generating and customizing the visor config file with `skywire cli config gen`.

For runtime configuration (changing a running visor without editing config), see [VISOR_RUNTIME.md](VISOR_RUNTIME.md).

## Quick Start

```bash
# Generate default config
skywire cli config gen

# Regenerate (preserves keys)
skywire cli config gen -r

# Show all flags (including hidden)
skywire cli config gen --all

# Print SKYENV template
skywire cli config gen -q
```

## Config Gen Help (`--all`)

```
skywire cli config gen --all

Generate a config file

	Config defaults file may also be specified with:
	SKYENV=/path/to/skywire.conf skywire-cli config gen
	print the SKYENV file template with:
	skywire-cli config gen -q

Flags:
  -n, --stdout                 write config to stdout
  -N, --squash                 output config without whitespace or newlines
  -o, --out string             output config: skywire-config.json
  -w, --hide                   dont print the config to the terminal :: show errors with -n flag
  -q, --envs                   show the conf template (reflects flags passed)
  -Q, --envout string          write conf template to file (reflects flags passed)
  -f, --force                  remove pre-existing config
  -r, --regen                  re-generate existing config & retain keys
  -x, --retainhv               retain existing hypervisors with regen
  -a, --url string             services conf url
  -t, --testenv                use test deployment
  -d, --dmsghttp               use only dmsg connection to skywire services
      --http                   use only http connection to skywire services
  -D, --dmsgconf string        dmsghttp-config path
  -b, --bestproto              best protocol based on location
      --nofetch                do not fetch the services from the service conf url
  -S, --svcconf string         fallback service configuration file
      --nodefaults             do not use hardcoded defaults for services
      --minsess int            number of dmsg servers to connect to (default 2)
  -y, --autoconn               disable autoconnect to public visors
  -z, --public                 publicize visor in service discovery
      --stcpr int              set tcp transport listening port
      --sudph int              set udp transport listening port
      --sync-tpd-data          enable transport discovery data sync
      --routesetup string      add route setup node PKs
      --tpsetup string         add transport setup node PKs
      --sn                     generate config for route setup node
      --calculate-routes       enable local route calculation
  -i, --ishv                   local hypervisor configuration
  -j, --hvpks string           list of public keys to add as hypervisor
  -c, --noauth                 disable authentication for hypervisor UI
  -e, --auth                   enable auth on hypervisor UI
      --dmsgpty string         add dmsgpty whitelist PKs
      --survey string          add survey whitelist PKs
  -l, --publicip               display visor ip in service discovery
  -m, --example-apps           add example apps to the config
      --external-apps          configure launcher apps as external processes
  -g, --disableapps string     comma separated list of apps to disable
      --binpath string         set bin_path
  -v, --servevpn               autostart vpn server (default true)
      --killsw string          vpn client killswitch
      --addvpn string          set vpn server public key for vpn client
      --vpnwl string           vpn server whitelist
      --secure string          vpn server secure mode
      --netifc string          VPN Server network interface
      --proxyclientpk string   set server public key for proxy client
      --startproxyclient       autostart proxy client
      --serveproxy             autostart proxy server (default true)
      --proxywl string         proxy server whitelist
      --servechat              autostart skychat (default true)
      --chataddr string        skychat local address (default ":8001")
      --rewardaddr string      skycoin reward address or xpub key
  -k, --os string              (linux / mac / win) paths (default "linux")
  -p, --pkg                    use path for package: /opt/skywire
  -u, --user                   use paths for user space
      --loglvl string          level of logging (default "info")
  -s, --sk cipher.SecKey       a random key is generated if unspecified
      --version string         custom version testing override
      --hvaddr string          hypervisor HTTP address
      --stun string            comma-separated list of STUN servers
      --timeout string         graceful shutdown timeout
      --regtimeout string      public visor registration timeout
      --maxtransports int      public visor max transports
      --muxroutes int          parallel mux routes per connection
      --cliaddr string         CLI RPC address
```

## SKYENV Config File

The config template printed by `-q` can be saved to `/etc/skywire.conf`
(or any path set via `SKYENV` env var). When `config gen` runs, it
sources this file to populate flag defaults.

```bash
# Generate template
skywire cli config gen -q > /etc/skywire.conf

# Edit the template
vi /etc/skywire.conf

# Generate config using template
SKYENV=/etc/skywire.conf skywire cli config gen
```

## SKYENV Variables

### Installation

| Variable | Type | Description |
|----------|------|-------------|
| `PKGENV` | bool | Use system package paths (`/opt/skywire`) |
| `USRENV` | bool | Use current user paths (`~/`) |
| `OUTPUT` | string | Output config file path |
| `BINPATH` | string | App binary path |
| `SVCCONF` | string | Service config file path override |
| `DMSGCONF` | string | DMSG HTTP config file path override |

### Deployment

| Variable | Type | Description |
|----------|------|-------------|
| `SVCCONFADDR` | string | Custom service config URL |
| `TESTENV` | bool | Use test deployment (ports offset +10000) |
| `DMSGHTTP` | bool | DMSG-only connections to services |
| `BESTPROTO` | bool | Auto-detect best protocol based on location |
| `MINDMSGSESS` | int | DMSG servers to connect to (0 = unlimited, default 2) |

### Transports

| Variable | Type | Description |
|----------|------|-------------|
| `VISORISPUBLIC` | bool | Accept incoming transports (public IP or port forwarded) |
| `DISABLEPUBLICAUTOCONN` | bool | Disable auto-connect to public visors |
| `TPSETUPPKS` | string | Transport setup node public keys (comma-separated) |
| `SYNCTPDDATA` | bool | Sync transport discovery data for local route calculation |
| `SUDPHPORT` | int | UDP port for SUDPH transports (0 = random) |
| `STCPRPORT` | int | TCP port for STCPR transports (0 = random) |

### Routing

| Variable | Type | Description |
|----------|------|-------------|
| `ROUTESETUPPKS` | string | Route setup node public keys (comma-separated) |
| `CALCULATEROUTES` | bool | Calculate routes locally instead of using route finder |

### Remote Access

| Variable | Type | Description |
|----------|------|-------------|
| `HYPERVISORPKS` | string | Remote hypervisor public keys (comma-separated) |
| `DMSGPTYPKS` | string | Public keys granted pseudoterminal access |
| `SURVEYPKS` | string | Public keys allowed to collect surveys |

### Hypervisor

| Variable | Type | Description |
|----------|------|-------------|
| `ISHYPERVISOR` | bool | Enable hypervisor web UI on this visor |
| `HVHTTPADDR` | string | Hypervisor HTTP address (default `:8000`) |

### Rewards

| Variable | Type | Description |
|----------|------|-------------|
| `REWARDSKYADDR` | string | Skycoin reward address or BIP44 account xpub key |

### Apps

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `DISPLAYNODEIP` | bool | false | Show node IP in service discovery |
| `VPNSERVER` | bool | true | Autostart VPN server |
| `VPNSERVERWL` | string | (empty) | VPN server whitelist (comma-separated PKs) |
| `PROXYSERVER` | bool | true | Autostart proxy server (skysocks) |
| `PROXYSERVERWL` | string | (empty) | Proxy server whitelist (comma-separated PKs) |
| `SKYCHAT` | bool | true | Autostart skychat |
| `SKYCHATADDR` | string | :8001 | Skychat local address |
| `PROXYCLIENTPK` | string | | Proxy client server public key |
| `STARTPROXYCLIENT` | bool | false | Autostart proxy client |
| `ADDVPNPK` | string | | VPN client server public key |
| `VPNKS` | bool | false | VPN client killswitch |

### Advanced Tuning

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `STUNSERVERS` | string | (from services) | STUN servers (comma-separated) |
| `SHUTDOWNTIMEOUT` | string | 10s | Graceful shutdown timeout |
| `REGTIMEOUT` | string | 10m | Public visor registration timeout |
| `MAXTRANSPORTS` | int | 1000 | Public visor max transports |
| `MUXROUTES` | int | 0 | Parallel mux routes per connection |
| `LOGLVL` | string | info | Log level (debug, info, warn, error) |
| `SK` | string | (random) | Secret key |

## JSON Config Sections

The generated `skywire-config.json` contains these top-level sections:

| Section | Description | Runtime equivalent |
|---------|-------------|--------------------|
| `dmsg` | DMSG client configuration | [DMSG management](VISOR_RUNTIME.md#dmsg-management) |
| `dmsgpty` | Pseudoterminal access | — |
| `transport` | Transport layer config | [Transport management](VISOR_RUNTIME.md#transport-management) |
| `routing` | Route setup nodes, route finder | [Route management](VISOR_RUNTIME.md#route-management) |
| `launcher` | App launcher, service discovery | [App management](VISOR_RUNTIME.md#app-management) |
| `dht` | Kademlia DHT (optional) | [DHT management](VISOR_RUNTIME.md#dht) |
| `rewards` | Reward system UI config | — |
| `hypervisor` | Hypervisor web UI | [Hypervisor](VISOR_RUNTIME.md#hypervisor) |

### DHT Configuration (optional)

DHT is enabled automatically when DMSG is available. The `dht` section
is only needed to customize behavior:

```json
{
  "dht": {
    "full_node": true,
    "bootstrap_pks": ["02abc...", "03def..."],
    "whitelisted_pks": ["02xyz..."],
    "trusted_pks": ["03uvw..."]
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `full_node` | bool | Store all DHT items regardless of XOR distance |
| `bootstrap_pks` | []string | Seed DHT nodes (default: deployment service PKs) |
| `whitelisted_pks` | []string | Publisher PKs whose data is never evicted |
| `trusted_pks` | []string | Publisher PKs with full replication unless abuse |

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
see [VISOR_RUNTIME.md](VISOR_RUNTIME.md).
