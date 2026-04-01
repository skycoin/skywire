# Visor Configuration Guide

This document covers how to generate, customize, and update the skywire visor configuration.

## Config Generation

Generate a config file:
```
skywire cli config gen
```

Print the config template (SKYENV format) to customize defaults:
```
skywire cli config gen -q
```

Show all available flags (including hidden advanced options):
```
skywire cli config gen --all
```

Generate to stdout (useful for piping/inspection):
```
skywire cli config gen -n
```

Regenerate an existing config (preserves keys and some settings):
```
skywire cli config gen -r
```

### SKYENV Config File

The config template printed by `-q` can be saved to `/etc/skywire.conf` (or any path set via `SKYENV` env var). When `config gen` runs, it sources this file to populate flag defaults.

```
SKYENV=/path/to/skywire.conf skywire cli config gen
```

## Config Template Variables

### Installation

| Variable | Type | Description |
|----------|------|-------------|
| `PKGENV` | bool | Use system package paths |
| `USRENV` | bool | Use current user paths |
| `OUTPUT` | string | Output config file path |
| `BINPATH` | string | App binary path |
| `SVCCONF` | string | Service config file path override |
| `DMSGCONF` | string | DMSG HTTP config file path override |

### Deployment

| Variable | Type | Description |
|----------|------|-------------|
| `SVCCONFADDR` | array | Custom service config URLs |
| `TESTENV` | bool | Use test deployment |
| `DMSGHTTP` | bool | Use DMSG-only connections to deployment |
| `BESTPROTO` | bool | Auto-detect best protocol based on location |
| `MINDMSGSESS` | int | Number of DMSG servers to connect to (0 = unlimited) |

### Transports

| Variable | Type | Description |
|----------|------|-------------|
| `VISORISPUBLIC` | bool | Accept incoming transports (requires public IP or port forwarding) |
| `DISABLEPUBLICAUTOCONN` | bool | Don't auto-connect to public visors |
| `TPSETUPPKS` | array | Transport setup node public keys |
| `SYNCTPDDATA` | bool | Enable transport discovery data sync |
| `SUDPHPORT` | int | UDP port for SUDPH transports |
| `STCPRPORT` | int | TCP port for STCPR transports |

### Routing

| Variable | Type | Description |
|----------|------|-------------|
| `ROUTESETUPPKS` | array | Route setup node public keys |
| `CALCULATEROUTES` | bool | Calculate routes locally instead of using route finder |

### Remote Access

| Variable | Type | Description |
|----------|------|-------------|
| `HYPERVISORPKS` | array | Remote hypervisor public keys |
| `DMSGPTYPKS` | array | Public keys granted pseudoterminal access |
| `SURVEYPKS` | array | Public keys allowed to collect surveys |

### Hypervisor

| Variable | Type | Description |
|----------|------|-------------|
| `ISHYPERVISOR` | bool | Enable hypervisor interface on this visor |

### Rewards

| Variable | Type | Description |
|----------|------|-------------|
| `REWARDSKYADDR` | string | Skycoin reward address or BIP44 account xpub key |

### Apps

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `DISPLAYNODEIP` | bool | false | Show node IP in service discovery |
| `VPNSERVER` | bool | true | Autostart VPN server |
| `VPNSERVERWL` | array | (empty) | VPN server whitelist (empty = allow all) |
| `PROXYSERVER` | bool | true | Autostart proxy server (skysocks) |
| `PROXYSERVERWL` | array | (empty) | Proxy server whitelist (empty = allow all) |
| `SKYCHAT` | bool | true | Autostart skychat |
| `SKYCHATADDR` | string | :8001 | Skychat local address |
| `PROXYCLIENTPK` | string | | Proxy client server public key |
| `STARTPROXYCLIENT` | bool | false | Autostart proxy client |
| `ADDVPNPK` | string | | VPN client server public key |
| `VPNKS` | bool | false | VPN client killswitch |
| `VPNSEVERSECURE` | string | | VPN server secure mode |
| `VPNSEVERNETIFC` | string | | VPN server network interface |

### Advanced Tuning

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `HVHTTPADDR` | string | :8000 | Hypervisor HTTP address |
| `STUNSERVERS` | array | (from services) | STUN servers for NAT traversal |
| `SHUTDOWNTIMEOUT` | string | 10s | Graceful shutdown timeout |
| `REGTIMEOUT` | string | 10m | Public visor registration timeout |
| `MAXTRANSPORTS` | int | 1000 | Public visor max transports |
| `MUXROUTES` | int | 0 | Parallel mux routes per connection |

### Miscellaneous

| Variable | Type | Description |
|----------|------|-------------|
| `SK` | string | Secret key (random if unset) |
| `VERSION` | string | Config version override |
| `LOGLVL` | string | Log level (debug, info, warn, error) |

## Runtime Configuration

The visor config can be updated while the visor is running using `skywire cli config update` subcommands. Changes take effect on visor restart unless applied via RPC.

### Update Service Endpoints

```
skywire cli config update -a
```

### Hypervisor

Add a remote hypervisor:
```
skywire cli config update hv --add-pks <public-key>
```

Reset hypervisor list:
```
skywire cli config update hv -r
```

### Proxy Server (Skysocks)

Set whitelist:
```
skywire cli config update ss --whitelist <pk1>,<pk2>
```

Reset proxy server config:
```
skywire cli config update ss -r
```

### VPN Server

Set whitelist:
```
skywire cli config update vpns --whitelist <pk1>,<pk2>
```

Set network interface:
```
skywire cli config update vpns --netifc eth0
```

Set autostart:
```
skywire cli config update vpns --autostart true
```

Reset VPN server config:
```
skywire cli config update vpns -r
```

### VPN Client

Set server:
```
skywire cli config update vpnc --add-server <public-key>
```

Set killswitch:
```
skywire cli config update vpnc --killsw true
```

Reset VPN client config:
```
skywire cli config update vpnc -r
```

### Proxy Client (Skysocks Client)

Set server:
```
skywire cli config update sc --add-server <public-key>
```

Reset proxy client config:
```
skywire cli config update sc -r
```

### Other Update Options

Set log level:
```
skywire cli config update --log-level debug
```

Set public autoconnect:
```
skywire cli config update --public-autoconn true
```

Set minimum hops:
```
skywire cli config update --set-minhop 1
```

## Live Runtime Commands (via RPC)

These commands change the running visor's state without modifying the config file.

### App Management

```
skywire cli visor app arg autostart <app-name> true|false
skywire cli visor app arg killswitch <app-name> true|false
skywire cli visor app arg secure <app-name> true|false
skywire cli visor app arg netifc <app-name> <interface|remove>
```

### Reward Address

Set reward address (skycoin address or xpub key):
```
skywire cli reward <address>
```

Read current reward address:
```
skywire cli reward -r
```

Delete reward address (opt out):
```
skywire cli reward -d
```

### Transport Management

Add a transport:
```
skywire cli tp add <remote-public-key>
```

Remove a transport:
```
skywire cli tp rm -i <transport-id>
```

Remove all transports:
```
skywire cli tp rm -a
```
