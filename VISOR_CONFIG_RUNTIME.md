# Visor Configuration — Runtime Changes

This document covers changing visor configuration on a running visor
via CLI/RPC. These commands interact with the running visor process
and take effect immediately without restarting.

For config file changes (config gen, config update, SKYENV), see
[VISOR_CONFIG_GEN.md](VISOR_CONFIG_GEN.md).

## Important Notes

### Config Persistence

**Runtime changes are not written to the config file.** They affect
the running visor only and are lost on restart. To make changes
permanent, set the corresponding SKYENV variable in
`/etc/skywire.conf` and regenerate the config. See
[VISOR_CONFIG_GEN.md](VISOR_CONFIG_GEN.md#skyenv-config-file).

**Package install/update runs `skywire autoconfig`** which
regenerates the config file. Any customization that isn't in the
SKYENV file will be overwritten. To disable:
```bash
echo "NOAUTOCONFIG=true" >> /etc/skywire.conf
```

**Warning:** If you disable autoconfig, you must manually update your
config when new versions are released. The reward system and uptime
tracker check the **config version** (not the binary version) — a
visor running a new binary with an old config may lose reward
eligibility. Run `skywire cli config gen -r` to regenerate while
preserving keys.

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
| `dmsg.discovery` | Not changeable at runtime |
| `dmsg.servers` | Not changeable at runtime |
| `dmsg.protocol` | Not changeable at runtime |

Config gen: [`MINDMSGSESS`](VISOR_CONFIG_GEN.md#deployment)

## Transport (`transport`)

| Config field | Runtime command |
|---|---|
| `transport.public_autoconnect` | `skywire cli tp auto on\|off` |
| `transport.stcpr_port` | Not changeable at runtime |
| `transport.sudph_port` | Not changeable at runtime |

Config gen: [`VISORISPUBLIC`](VISOR_CONFIG_GEN.md#transports), [`STCPRPORT`](VISOR_CONFIG_GEN.md#transports)

## Routing (`routing`)

| Config field | Runtime command |
|---|---|
| `routing.min_hops` | `skywire cli route minhops <n>` |
| `routing.calculate_routes` | `skywire cli route calc --enable\|--disable` |
| `routing.route_setup_nodes` | Not changeable at runtime |

Config gen: [`ROUTESETUPPKS`](VISOR_CONFIG_GEN.md#routing), [`CALCULATEROUTES`](VISOR_CONFIG_GEN.md#routing)

## Apps (`launcher`)

| Config field | Runtime command |
|---|---|
| `launcher.apps[].auto_start` | `skywire cli visor app arg autostart <name> true\|false` |
| app killswitch | `skywire cli visor app arg killswitch <name> true\|false` |
| app secure mode | `skywire cli visor app arg secure <name> true\|false` |
| app network interface | `skywire cli visor app arg netifc <name> <iface\|remove>` |
| app passcode | `skywire cli visor app arg passcode <name> <code>` |

### Resolving Proxies (`dmsg_web` / `skynet_web`)

| Config field | Runtime command |
|---|---|
| `dmsg_web.enable` | `skywire cli visor proxies set dmsg on\|off` |
| `skynet_web.enable` | `skywire cli visor proxies set skynet on\|off` |
| upstream SOCKS5 | `skywire cli visor proxies upstream <dmsg\|skynet> <addr>` |

Config gen: [`VPNSERVER`](VISOR_CONFIG_GEN.md#apps), [`PROXYSERVER`](VISOR_CONFIG_GEN.md#apps)

## Hypervisor (`hypervisor`)

| Config field | Runtime command |
|---|---|
| `hypervisor.enable` | `skywire cli visor hv enable\|disable` |
| `hypervisors` (add remote HV) | `skywire cli visor hv add <pk>` |

Config gen: [`ISHYPERVISOR`](VISOR_CONFIG_GEN.md#hypervisor), [`HYPERVISORPKS`](VISOR_CONFIG_GEN.md#remote-access)

## Log Level (`log_level`)

| Config field | Runtime command |
|---|---|
| `log_level` | Not changeable at runtime |

Config gen: [`LOGLVL`](VISOR_CONFIG_GEN.md#advanced-tuning)

## Survey Whitelist (`survey_whitelist`)

| Config field | Runtime command |
|---|---|
| `survey_whitelist` | Not changeable at runtime |

Config gen: [`SURVEYPKS`](VISOR_CONFIG_GEN.md#remote-access)

## Reward Address

| Config field | Runtime command |
|---|---|
| `reward_address` | `skywire cli reward <address>` |

Config gen: [`REWARDSKYADDR`](VISOR_CONFIG_GEN.md#rewards)

## DHT (`dht`)

| Config field | Runtime command |
|---|---|
| `dht.full_node` | `skywire cli visor dht full-node on\|off` |
| `dht.bootstrap_pks` | Not changeable at runtime |
| `dht.whitelisted_pks` | Not changeable at runtime |
| `dht.trusted_pks` | Not changeable at runtime |

Config gen: [DHT configuration](VISOR_CONFIG_GEN.md#dht-configuration-optional)

## Skynet Port Forwarding

Port forwarding is stored in `local/forwarded_ports.json`, not in
the visor config. Changes are both immediate and persistent.

| Config field | Runtime command |
|---|---|
| forwarded ports | `skywire cli skynet port add\|rm\|ls` |

See also: [Skynet forwarding guide](docs/skywire_forwarding.md)
