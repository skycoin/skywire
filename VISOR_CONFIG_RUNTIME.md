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
| — (connect to all) | `skywire cli dmsg connect-all` |
| — (force reconnect) | `skywire cli dmsg diag reconnect` |
| — (reset port space) | `skywire cli dmsg diag porter-reset` |
| — (check port usage) | `skywire cli dmsg diag porter` |
| `dmsg.discovery` | Not changeable at runtime |
| `dmsg.servers` | Not changeable at runtime |
| `dmsg.protocol` | Not changeable at runtime |

Config gen: [`MINDMSGSESS`](VISOR_CONFIG_GEN.md#deployment)

## Transport (`transport`)

| Config field | Runtime command |
|---|---|
| — (add transport) | `skywire cli tp add <pk> [--type stcpr\|sudph\|dmsg]` |
| — (remove transport) | `skywire cli tp rm -i <id>` or `skywire cli tp rm -a` |
| `transport.public_autoconnect` | Not changeable at runtime |
| `transport.stcpr_port` | Not changeable at runtime |
| `transport.sudph_port` | Not changeable at runtime |

Config gen: [`VISORISPUBLIC`](VISOR_CONFIG_GEN.md#transports), [`STCPRPORT`](VISOR_CONFIG_GEN.md#transports)

## Routing (`routing`)

| Config field | Runtime command |
|---|---|
| — (view rules) | `skywire cli route` |
| — (add rule) | `skywire cli route add a <pk> <port>` |
| — (remove rule) | `skywire cli route rm <id>` |
| — (RSN stats) | `skywire cli route rsn-stats [--reset]` |
| `routing.min_hops` | Not changeable at runtime |
| `routing.route_setup_nodes` | Not changeable at runtime |

Config gen: [`ROUTESETUPPKS`](VISOR_CONFIG_GEN.md#routing), [`CALCULATEROUTES`](VISOR_CONFIG_GEN.md#routing)

## Apps (`launcher`)

| Config field | Runtime command |
|---|---|
| — (list apps) | `skywire cli visor app ls` |
| — (start app) | `skywire cli visor app start <name>` |
| — (stop app) | `skywire cli visor app stop <name>` |
| `launcher.apps[].auto_start` | `skywire cli visor app arg autostart <name> true\|false` |
| app killswitch | `skywire cli visor app arg killswitch <name> true\|false` |
| app secure mode | `skywire cli visor app arg secure <name> true\|false` |
| app network interface | `skywire cli visor app arg netifc <name> <iface\|remove>` |
| app passcode | `skywire cli visor app arg passcode <name> <code>` |

### VPN

| Action | Runtime command |
|---|---|
| Start VPN client | `skywire cli vpn start` |
| Stop VPN client | `skywire cli vpn stop` |
| VPN client status | `skywire cli vpn status` |
| Start VPN server | `skywire cli vpn server start` |
| Stop VPN server | `skywire cli vpn server stop` |
| VPN server status | `skywire cli vpn server status` |

Config gen: [`VPNSERVER`](VISOR_CONFIG_GEN.md#apps), [`ADDVPNPK`](VISOR_CONFIG_GEN.md#apps), [`VPNKS`](VISOR_CONFIG_GEN.md#apps)

### Proxy (Skysocks)

| Action | Runtime command |
|---|---|
| Start proxy server | `skywire cli proxy server start` |
| Stop proxy server | `skywire cli proxy server stop` |
| Start proxy client | `skywire cli proxy start` |
| Stop proxy client | `skywire cli proxy stop` |
| Proxy status | `skywire cli proxy status` |

Config gen: [`PROXYSERVER`](VISOR_CONFIG_GEN.md#apps), [`PROXYCLIENTPK`](VISOR_CONFIG_GEN.md#apps)

## Hypervisor (`hypervisor`)

| Config field | Runtime command |
|---|---|
| `hypervisor.enable` | `skywire cli visor hv enable\|disable` |
| — (status) | `skywire cli visor hv status` |
| — (open UI) | `skywire cli visor hv ui` |
| `hypervisors` | Not changeable at runtime |

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
| — (read) | `skywire cli reward -r` |
| — (delete) | `skywire cli reward -d` |

Config gen: [`REWARDSKYADDR`](VISOR_CONFIG_GEN.md#rewards)

## DHT (`dht`)

| Config field | Runtime command |
|---|---|
| `dht.full_node` | `skywire cli visor dht full-node on\|off` |
| — (status) | `skywire cli visor dht status` |
| — (get value) | `skywire cli visor dht get <pk> [salt]` |
| — (put value) | `skywire cli visor dht put <value> [salt]` |
| `dht.bootstrap_pks` | Not changeable at runtime |
| `dht.whitelisted_pks` | Not changeable at runtime |
| `dht.trusted_pks` | Not changeable at runtime |

Config gen: [DHT configuration](VISOR_CONFIG_GEN.md#dht-configuration-optional)

## Skynet Port Forwarding

Port forwarding is stored in `local/forwarded_ports.json`, not in
the visor config. Changes are both immediate and persistent.

| Action | Runtime command |
|---|---|
| Forward a port | `skywire cli skynet port add <port> [--local-port <n>] [--label <s>]` |
| Remove a port | `skywire cli skynet port rm <port>` |
| List ports | `skywire cli skynet port ls` |
| Website on port 80 | `skywire cli skynet port add 80 --proxy-addr 127.0.0.1:<port>` |

See also: [Skynet forwarding guide](docs/skywire_forwarding.md)
