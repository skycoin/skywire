# Skywire merged binary

`cmd/skywire/skywire.go` represents a merged binary which integrates the skywire-cli, skywire-visor, setup-node, and app binaries.



```
$ skywire

	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
	└─┐├┴┐└┬┘││││├┬┘├┤
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘

Available Commands:
  visor        Skywire Visor
  cli          Command Line Interface for skywire
  sn           Route Setup Node for skywire
  app          skywire native applications

Flags:
  -v, --version   version for skywire

```

```
$ skywire visor --help


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

```
$ skywire sn --help

	┌─┐┌─┐┌┬┐┬ ┬┌─┐   ┌┐┌┌─┐┌┬┐┌─┐
	└─┐├┤  │ │ │├─┘───││││ │ ││├┤
	└─┘└─┘ ┴ └─┘┴     ┘└┘└─┘─┴┘└─┘



Flags:
  -m, --metrics string   address to bind metrics API to
  -i, --stdin            read config from STDIN
      --syslog string    syslog server address. E.g. localhost:514
      --tag string       logging tag (default "setup_node")


```

Refer to the existing documentation for separate binaries until further documentation for the merged binaries are generated. The command structure is otherwise identical.
