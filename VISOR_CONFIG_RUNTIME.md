# Visor Configuration — Runtime Changes

This document covers changing visor configuration on a running visor
via CLI/RPC. Changes take effect immediately without restarting.

For config file generation, see [VISOR_CONFIG_GEN.md](VISOR_CONFIG_GEN.md).

## Inspecting the Current Config

```bash
# Show the full running config
skywire cli config show

# Show a specific section
skywire cli config show .dmsg
skywire cli config show .routing
skywire cli config show .transport

# Show a specific field
skywire cli config show .dmsg.sessions_count
skywire cli config show .transport.public_autoconnect
skywire cli config show .log_level

# Query nested structures
skywire cli config show '.launcher.apps[] | .name'
skywire cli config show '.launcher.apps[] | select(.auto_start == true) | .name'
skywire cli config show .routing.route_setup_nodes

# Show config file path
skywire cli config show --path
```

The filter argument uses [jq syntax](https://jqlang.github.io/jq/manual/).

## DMSG (`dmsg`)

| Config field | Runtime command |
|---|---|
| `dmsg.sessions_count` | `skywire cli dmsg set-sessions <n>` |
| — (connect to all) | `skywire cli dmsg connect-all` |
| — (force reconnect) | `skywire cli dmsg diag reconnect` |
| — (reset port space) | `skywire cli dmsg diag porter-reset` |
| — (check port usage) | `skywire cli dmsg diag porter` |

Config gen: [`MINDMSGSESS`](VISOR_CONFIG_GEN.md#deployment)

## Transport (`transport`)

| Config field | Runtime command |
|---|---|
| `transport.public_autoconnect` | `skywire cli config update --public-autoconn true\|false` (persistent) |
| `transport.stcpr_port` | Config gen only: [`STCPRPORT`](VISOR_CONFIG_GEN.md#transports) |
| `transport.sudph_port` | Config gen only: [`SUDPHPORT`](VISOR_CONFIG_GEN.md#transports) |
| — (add transport) | `skywire cli tp add <pk> [--type stcpr\|sudph\|dmsg]` |
| — (remove transport) | `skywire cli tp rm -i <id>` or `skywire cli tp rm -a` |

Config gen: [`VISORISPUBLIC`](VISOR_CONFIG_GEN.md#transports), [`DISABLEPUBLICAUTOCONN`](VISOR_CONFIG_GEN.md#transports)

## Routing (`routing`)

| Config field | Runtime command |
|---|---|
| `routing.min_hops` | `skywire cli config update --set-minhop <n>` (persistent) |
| `routing.route_setup_nodes` | Config gen only: [`ROUTESETUPPKS`](VISOR_CONFIG_GEN.md#routing) |
| — (view rules) | `skywire cli route` |
| — (add rule) | `skywire cli route add a <pk> <port>` |
| — (remove rule) | `skywire cli route rm <id>` |
| — (RSN stats) | `skywire cli route rsn-stats [--reset]` |

Config gen: [`CALCULATEROUTES`](VISOR_CONFIG_GEN.md#routing)

## Launcher / Apps (`launcher`)

| Config field | Runtime command |
|---|---|
| `launcher.apps[].auto_start` | `skywire cli visor app arg autostart <name> true\|false` |
| — (start app) | `skywire cli visor app start <name>` |
| — (stop app) | `skywire cli visor app stop <name>` |
| — (list apps) | `skywire cli visor app ls` |

### VPN Server

| Config field | Runtime command |
|---|---|
| VPN server autostart | `skywire cli config update vpns --autostart true\|false` (persistent) |
| VPN server whitelist | `skywire cli config update vpns --whitelist <pk1>,<pk2>` (persistent) |
| VPN server interface | `skywire cli config update vpns --netifc <iface>` (persistent) |
| VPN server secure | `skywire cli visor app arg secure vpn-server true\|false` |
| — (start/stop) | `skywire cli vpn server start\|stop\|status` |

Config gen: [`VPNSERVER`](VISOR_CONFIG_GEN.md#apps), [`VPNSERVERWL`](VISOR_CONFIG_GEN.md#apps)

### VPN Client

| Config field | Runtime command |
|---|---|
| VPN client server | `skywire cli config update vpnc --add-server <pk>` (persistent) |
| VPN client killswitch | `skywire cli config update vpnc --killsw true\|false` (persistent) |
| VPN client killswitch | `skywire cli visor app arg killswitch vpn-client true\|false` |
| — (start/stop) | `skywire cli vpn start\|stop\|status` |

Config gen: [`ADDVPNPK`](VISOR_CONFIG_GEN.md#apps), [`VPNKS`](VISOR_CONFIG_GEN.md#apps)

### Proxy Server (Skysocks)

| Config field | Runtime command |
|---|---|
| Proxy server whitelist | `skywire cli config update ss --whitelist <pk1>,<pk2>` (persistent) |
| Proxy server autostart | `skywire cli config update ss --autostart true\|false` (persistent) |
| — (start/stop) | `skywire cli proxy server start\|stop\|status` |

Config gen: [`PROXYSERVER`](VISOR_CONFIG_GEN.md#apps), [`PROXYSERVERWL`](VISOR_CONFIG_GEN.md#apps)

### Proxy Client (Skysocks Client)

| Config field | Runtime command |
|---|---|
| Proxy client server | `skywire cli config update sc --add-server <pk>` (persistent) |
| — (start/stop) | `skywire cli proxy start\|stop\|status` |

Config gen: [`PROXYCLIENTPK`](VISOR_CONFIG_GEN.md#apps), [`STARTPROXYCLIENT`](VISOR_CONFIG_GEN.md#apps)

## Hypervisor (`hypervisor`)

| Config field | Runtime command |
|---|---|
| `hypervisor.enable` | `skywire cli visor hv enable\|disable` |
| `hypervisors` (remote HV PKs) | `skywire cli config update hv --add-pks <pk>` (persistent) |
| `hypervisors` (reset) | `skywire cli config update hv -r` (persistent) |

Config gen: [`ISHYPERVISOR`](VISOR_CONFIG_GEN.md#hypervisor), [`HYPERVISORPKS`](VISOR_CONFIG_GEN.md#remote-access)

## Survey Whitelist (`survey_whitelist`)

| Config field | Runtime command |
|---|---|
| `survey_whitelist` | Config gen only: [`SURVEYPKS`](VISOR_CONFIG_GEN.md#remote-access) |

## Log Level (`log_level`)

| Config field | Runtime command |
|---|---|
| `log_level` | `skywire cli config update --log-level debug\|info\|warn\|error` (persistent) |

Config gen: [`LOGLVL`](VISOR_CONFIG_GEN.md#advanced-tuning)

## Reward Address

| Config field | Runtime command |
|---|---|
| `reward_address` | `skywire cli reward <address>` |
| — (read) | `skywire cli reward -r` |
| — (delete) | `skywire cli reward -d` |

Config gen: [`REWARDSKYADDR`](VISOR_CONFIG_GEN.md#rewards)

## DHT (`dht`)

| Config field | Runtime command |
|---|---|
| `dht.full_node` | `skywire cli visor dht full-node on\|off` |
| — (status) | `skywire cli visor dht status` |

Config gen: [DHT configuration](VISOR_CONFIG_GEN.md#dht-configuration-optional)

## Skynet Port Forwarding

Port forwarding is stored in `local/forwarded_ports.json`, not in
the visor config. All changes are runtime and persistent.

| Setting | Runtime command |
|---|---|
| Forward a port | `skywire cli skynet port add <port> [--local-port <n>] [--label <s>]` |
| Remove a port | `skywire cli skynet port rm <port>` |
| List ports | `skywire cli skynet port ls` |
| Website on port 80 | `skywire cli skynet port add 80 --proxy-addr 127.0.0.1:<port>` |

See also: [Skynet forwarding guide](docs/skywire_forwarding.md)

## Persistent vs Immediate

| Marker | Meaning |
|---|---|
| (no marker) | Takes effect immediately via RPC, not written to config file |
| (persistent) | Written to config file, applies on visor restart |
| Config gen only | Can only be set during `skywire cli config gen`, not at runtime |

Some commands marked (persistent) write to the config file using
`skywire cli config update`. The running visor does NOT pick up these
changes until restarted. To apply changes immediately AND persistently,
use both the runtime RPC command and the config update command.
