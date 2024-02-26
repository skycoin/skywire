# Skywire Deployment Merged Binary

`../skywire/skywire.go` represents a merged binary which integrates the skywire-cli, skywire-visor, setup-node, and the native visor applications (proxy, vpn, skychat).


The skywire-deployment [`skywire.go`](skywire.go) additionally includes [skywire services](https://github.com/skycoin/skywire-services), [skywire service discovery](https://github.com/skycoin/skycoin-service-discovery), and the full compliment of [dmsg utilities](https://github.com/skycoin/dmsg). Refer to the existing documentation on these, or the documentation in [skywire deployment](https://github.com/skycoin/skywire-deployment) repo.

Top level menu
```
$ skywire

	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
	└─┐├┴┐└┬┘││││├┬┘├┤
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘

Available Commands:
  visor        Skywire Visor
  cli          Command Line Interface for skywire
  svc          Skywire services
  dmsg         Dmsg services & utilities
  app          skywire native applications

Flags:
  -v, --version   version for skywire


```

Visor help menu
```
$ skywire  visor --help

	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┬  ┬┬┌─┐┌─┐┬─┐
	└─┐├┴┐└┬┘││││├┬┘├┤───└┐┌┘│└─┐│ │├┬┘
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘   └┘ ┴└─┘└─┘┴└─

 Flags:
  -c, --config string    config file to use (default): skywire-config.json
  -C, --confarg string   supply config as argument
  -b, --browser          open hypervisor ui in default web browser
      --systray          run as systray
  -i, --hvui             run as hypervisor *
      --all              show all flags
      --csrf             Request a CSRF token for sensitive hypervisor API requests (default true)
  -v, --version          version for visor

```

Cli help menu
```
$ skywire cli

	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┌─┐┬  ┬
	└─┐├┴┐└┬┘││││├┬┘├┤───│  │  │
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  └─┘┴─┘┴

Usage:
  skywire cli

Available Commands:
  config       Generate or update a skywire config
  dmsgpty      Interact with remote visors
  visor        Query the Skywire Visor
  vpn          VPN client
  ut           query uptime tracker
  fwd          Control skyforwarding
  rev          reverse proxy skyfwd
  reward       skycoin reward address
  rewards      calculate rewards from uptime data & collected surveys
  survey       system survey
  rtfind       Query the Route Finder
  rtree        map of transports on the skywire network
  mdisc        Query remote DMSG Discovery
  completion   Generate completion script
  log          survey & transport log collection
  proxy        Skysocks client
  tree         subcommand tree
  doc          generate markdown docs

Flags:
  -v, --version   version for cli

```

Skywire deployment services help menu - includes service discovery
```
$ skywire svc

 ┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐┌─┐
 └─┐├┴┐└┬┘││││├┬┘├┤───└─┐├┤ ├┬┘└┐┌┘││  ├┤ └─┐
 └─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  └─┘└─┘┴└─ └┘ ┴└─┘└─┘└─┘

Available Commands:
 sn           Route Setup Node for skywire
 tpd          Transport Discovery Server for skywire
 tps          Transport setup server for skywire
 ar           Address Resolver Server for skywire
 rf           Route Finder Server for skywire
 cb           Config Bootstrap Server for skywire
 kg           skywire keys generator, prints pub-key and sec-key
 lc           Liveness checker of the deployment.
 nv           Node Visualizer Server for skywire
 swe          skywire environment generator
 sd           Service discovery server
 mn           Network monitor for skywire VPN and Visor.
 pvm          Public Visor monitor.
 skysocksmon  Skysocks monitor.
 vpnmon       VPN monitor.

Flags:
 -v, --version   version for svc

```

Dmsg utilities help menu
```
$ skywire dmsg

	┌┬┐┌┬┐┌─┐┌─┐
	 │││││└─┐│ ┬
	─┴┘┴ ┴└─┘└─┘

Available Commands:
  pty          Dmsg pseudoterminal (pty)
  disc         DMSG Discovery Server
  server       DMSG Server
  http         DMSG http file server
  curl         DMSG curl utility
  web          DMSG resolving proxy & browser client
  proxy        DMSG socks5 proxy server / client

```

Dmsg pty help menu
```
$ skywire dmsg pty

	┌─┐┌┬┐┬ ┬
	├─┘ │ └┬┘
	┴   ┴  ┴

Available Commands:
  cli          DMSG pseudoterminal command line interface
  host         DMSG host for pseudoterminal command line interface
  ui           DMSG pseudoterminal GUI

```

Skywire native apps help menu
```
$ skywire app

	┌─┐┌─┐┌─┐┌─┐
	├─┤├─┘├─┘└─┐
	┴ ┴┴  ┴  └─┘

Available Commands:
  vpn-server       skywire vpn server application
  vpn-client       skywire vpn client application
  skysocks-client  skywire socks5 proxy client application
  skysocks         skywire socks5 proxy server application
  skychat          skywire chat application

```
