# Visor Runtime Configuration

This document covers CLI commands that manage a running visor via RPC.
These changes take effect immediately without restarting the visor.

For config file generation and persistent configuration, see
[VISOR_CONFIG_GEN.md](VISOR_CONFIG_GEN.md).

## Visor Info

```bash
skywire cli visor info               # visor summary (PK, version, uptime)
skywire cli visor pk                 # print visor public key
skywire cli visor ver                # print visor version
skywire cli visor ip                 # print visor's public IP
skywire cli visor ports              # list registered ports
skywire cli visor ready              # check if visor is fully initialized
```

## DMSG Management

```bash
skywire cli dmsg sessions            # list connected DMSG server sessions
skywire cli dmsg set-sessions <n>    # set target number of DMSG sessions
skywire cli dmsg connect-all         # connect to all available DMSG servers
skywire cli dmsg probe <pk> <port>   # probe a remote visor's DMSG port
skywire cli dmsg curl <url>          # HTTP request over DMSG
```

### DMSG Diagnostics

```bash
skywire cli dmsg diag porter         # show ephemeral port reservation counts
skywire cli dmsg diag porter-reset   # free all ephemeral ports (recover from exhaustion)
skywire cli dmsg diag reconnect      # force-close all DMSG sessions and reconnect
```

See also: [DMSG sessions in config](VISOR_CONFIG_GEN.md#deployment) (`MINDMSGSESS`)

## Transport Management

```bash
skywire cli tp ls                    # list local transports
skywire cli tp add <pk>              # add transport to remote visor
skywire cli tp add <pk> --type stcpr # add specific transport type
skywire cli tp rm -i <id>            # remove transport by ID
skywire cli tp rm -a                 # remove all transports
skywire cli tp auto                  # show autoconnect status
skywire cli tp disc <pk>             # query transport discovery for a PK
skywire cli tp id <id>               # query transport by ID
skywire cli tp sync                  # sync transport discovery data locally
```

### Transport Network Statistics

```bash
skywire cli tp tree                  # transport tree from TPD (top visors, counts)
skywire cli tp tree --stats          # with type breakdown
skywire cli tp tpd-stats             # per-visor transport counts by type
skywire cli tp net-stats             # network-wide transport statistics
skywire cli tp metrics               # per-visor bandwidth/latency (default: 1 day)
skywire cli tp metrics --days 7      # 7-day bandwidth
skywire cli tp metrics --days 0      # all-time bandwidth
skywire cli tp metrics --by-transport # per-transport bandwidth
skywire cli tp uptime                # transport uptime data
```

See also: [Transport config](VISOR_CONFIG_GEN.md#transports) (`STCPRPORT`, `SUDPHPORT`, `VISORISPUBLIC`)

## Route Management

```bash
skywire cli route                    # list routing rules (via visor RPC)
skywire cli route groups             # list active route groups
skywire cli route add a <pk> <port>  # add forward rule
skywire cli route rm <id>            # remove routing rule
skywire cli route calc <pk>          # calculate route to destination
skywire cli route find <pk>          # find route via route finder
skywire cli route rsn-stats          # embedded route setup node statistics
skywire cli route rsn-stats --reset  # reset RSN counters
```

See also: [Routing config](VISOR_CONFIG_GEN.md#routing) (`ROUTESETUPPKS`, `CALCULATEROUTES`)

## Skynet Port Forwarding

Forward local TCP ports over skynet and/or DMSG:

```bash
skywire cli skynet port ls                              # list forwarded ports
skywire cli skynet port add 8080                        # forward port 8080
skywire cli skynet port add 80 --local-port 3000        # forward local:3000 to skynet:80
skywire cli skynet port add 80 --proxy-addr 127.0.0.1:3000  # reverse proxy on port 80
skywire cli skynet port add 8080 --label "My App" --desc "Dashboard"
skywire cli skynet port add 3000 --dmsg=false           # skynet only (no DMSG)
skywire cli skynet port rm 8080                         # remove forwarded port
```

Port forwarding persists to `local/forwarded_ports.json` (not the visor config).

### Skynet HTTP Requests

```bash
skywire cli skynet curl skynet://<pk>/path              # GET request over skynet
skywire cli skynet curl skynet://<pk>:<port>/path       # with port
skywire cli skynet curl -d '{"key":"val"}' skynet://<pk>/endpoint  # POST
skywire cli skynet curl -o file skynet://<pk>/download  # save to file
```

### Website Hosting

```bash
# Serve static files on skynet port 80
skywire cli util serve /path/to/site                    # starts local HTTP server
skywire cli skynet port add 80 --proxy-addr 127.0.0.1:<port> --label "My Site"
```

See also: [Skynet forwarding guide](docs/skywire_forwarding.md)

## DHT

The DHT is enabled automatically when DMSG is available.

```bash
skywire cli visor dht status           # show DHT node status (peers, items, tiers)
skywire cli visor dht get <pk> [salt]  # retrieve value from DHT
skywire cli visor dht put <val> [salt] # publish value under this visor's key
skywire cli visor dht full-node on     # enable full node mode (store everything)
skywire cli visor dht full-node off    # disable full node mode (store nearby only)
```

See also: [DHT config](VISOR_CONFIG_GEN.md#dht-configuration-optional) (`full_node`, `bootstrap_pks`)

## App Management

```bash
skywire cli visor app ls                              # list apps and status
skywire cli visor app start <name>                    # start an app
skywire cli visor app stop <name>                     # stop an app
skywire cli visor app register <name> --port <port>   # register an app
skywire cli visor app deregister <name>               # deregister an app
skywire cli visor app log <name>                      # show app logs
```

### App Arguments

```bash
skywire cli visor app arg autostart <name> true|false
skywire cli visor app arg killswitch <name> true|false
skywire cli visor app arg secure <name> true|false
skywire cli visor app arg netifc <name> <interface|remove>
skywire cli visor app arg passcode <name> <code>
```

See also: [App config](VISOR_CONFIG_GEN.md#apps) (`VPNSERVER`, `PROXYSERVER`, `SKYCHAT`)

## Proxy Management

```bash
skywire cli proxy start                    # start resolving proxy
skywire cli proxy stop                     # stop resolving proxy
skywire cli proxy status                   # show proxy status
skywire cli proxy list                     # list proxy routes
skywire cli proxy test <url>               # test proxy resolution
```

### Proxy Server

```bash
skywire cli proxy server start             # start proxy server (skysocks)
skywire cli proxy server stop              # stop proxy server
skywire cli proxy server status            # show proxy server status
```

### Proxy Routes

```bash
skywire cli proxy route add <domain> <pk>  # add route
skywire cli proxy route remove <domain>    # remove route
```

## VPN

```bash
skywire cli vpn start                      # start VPN client
skywire cli vpn stop                       # stop VPN client
skywire cli vpn status                     # show VPN status
skywire cli vpn list                       # list available VPN servers
skywire cli vpn server start               # start VPN server
skywire cli vpn server stop                # stop VPN server
skywire cli vpn server status              # show VPN server status
```

See also: [VPN config](VISOR_CONFIG_GEN.md#apps) (`VPNSERVER`, `ADDVPNPK`, `VPNKS`)

## Hypervisor

```bash
skywire cli visor hv status                # hypervisor status
skywire cli visor hv enable                # enable hypervisor
skywire cli visor hv disable               # disable hypervisor
skywire cli visor hv pk                    # hypervisor public key
skywire cli visor hv cpk                   # connected hypervisor PKs
skywire cli visor hv ui                    # open hypervisor UI in browser
```

See also: [Hypervisor config](VISOR_CONFIG_GEN.md#hypervisor) (`ISHYPERVISOR`, `HVHTTPADDR`)

## Reward Address

```bash
skywire cli reward <address>               # set reward address (skycoin or xpub)
skywire cli reward -r                      # read current reward address
skywire cli reward -d                      # delete reward address (opt out)
skywire cli reward rules                   # show reward eligibility rules
```

See also: [Rewards config](VISOR_CONFIG_GEN.md#rewards) (`REWARDSKYADDR`)

## Service Discovery

```bash
skywire cli sd                             # aggregated network stats from all discoveries
skywire cli svc health                     # health check all deployment services
skywire cli svc tpd bandwidth             # network bandwidth statistics
skywire cli svc tpd versions             # visor version distribution
skywire cli svc tpd per-key-stats        # per-visor transport/bandwidth stats
skywire cli svc dmsgd all-servers        # list DMSG servers
skywire cli svc dmsgd clients            # count DMSG clients
skywire cli mdisc servers                 # list DMSG discovery servers
skywire cli mdisc entry <pk>              # look up DMSG discovery entry
skywire cli pv                            # list public visors
```

## Visor Lifecycle

```bash
skywire cli visor halt                     # graceful shutdown
skywire cli visor start                    # start visor (if not running)
skywire cli visor reinit                   # reinitialize visor subsystems
```

## Utilities

```bash
skywire cli util serve <dir>               # serve static files over HTTP
skywire cli util got dl <url>              # download file (chunked)
skywire cli util got req <method> <url>    # HTTP request
skywire cli util got head <url>            # HTTP HEAD request
skywire cli util jq <filter>              # jq-like JSON processor
skywire cli util edit <file>              # terminal text editor
```

## Documentation Generation

```bash
skywire help -d                            # generate markdown docs for all commands
skywire help -t                            # print command tree
skywire cli help -d                        # docs for CLI subtree
skywire cli help -r                        # recursive help text
```
