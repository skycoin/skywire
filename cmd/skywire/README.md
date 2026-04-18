# Skywire

## Subcommand Tree

```
skywire
├── app
│   ├── skychat
│   ├── skynet-client
│   ├── skynet-srv
│   ├── skysocks
│   ├── skysocks-client
│   ├── vpn-client
│   └── vpn-server
├── cli
│   ├── completion
│   ├── config
│   │   ├── check-pk
│   │   ├── gen
│   │   ├── gen-keys
│   │   ├── parse
│   │   ├── pk
│   │   ├── show
│   │   └── update
│   │       ├── hv
│   │       ├── sc
│   │       ├── ss
│   │       ├── svc
│   │       ├── vpnc
│   │       └── vpns
│   ├── dmsg
│   │   ├── connect-all
│   │   ├── curl
│   │   ├── probe
│   │   ├── pty
│   │   │   ├── list
│   │   │   ├── start
│   │   │   ├── ui
│   │   │   └── url
│   │   ├── sessions
│   │   └── set-sessions
│   ├── gotop
│   ├── log
│   │   ├── st
│   │   └── tp
│   ├── mdisc
│   │   ├── entry
│   │   └── servers
│   ├── proxy
│   │   ├── list
│   │   ├── route
│   │   │   ├── add
│   │   │   └── remove
│   │   ├── server
│   │   │   ├── start
│   │   │   ├── status
│   │   │   └── stop
│   │   ├── start
│   │   ├── status
│   │   ├── stop
│   │   └── test
│   ├── pv
│   ├── reward
│   │   └── rules
│   ├── rewards
│   │   ├── bot
│   │   ├── bw-collect
│   │   ├── loginchain
│   │   ├── script
│   │   │   ├── getlogs
│   │   │   └── reward
│   │   ├── svc
│   │   ├── systemd
│   │   ├── tp-collect
│   │   └── ui
│   ├── rg
│   │   └── ls
│   ├── route
│   │   ├── add
│   │   │   ├── a
│   │   │   ├── b
│   │   │   └── c
│   │   ├── calc
│   │   ├── find
│   │   ├── groups
│   │   ├── rm
│   │   └── rsn-stats
│   ├── sd
│   ├── skychat
│   │   ├── listen
│   │   └── send
│   ├── skynet
│   │   ├── curl
│   │   ├── port
│   │   │   ├── add
│   │   │   ├── ls
│   │   │   └── rm
│   │   ├── srv
│   │   │   ├── start
│   │   │   ├── status
│   │   │   └── stop
│   │   ├── start
│   │   ├── status
│   │   └── stop
│   ├── survey
│   ├── svc
│   │   ├── ar
│   │   ├── dmsgd
│   │   │   ├── all-servers
│   │   │   ├── clients
│   │   │   └── server-clients
│   │   ├── health
│   │   ├── nm
│   │   └── tpd
│   │       ├── bandwidth
│   │       ├── bandwidth-tp
│   │       ├── metrics-tp
│   │       ├── metrics-visor
│   │       ├── per-key-stats
│   │       ├── stats
│   │       ├── versions
│   │       ├── versions-pk
│   │       └── visor-stats
│   ├── tp
│   │   ├── add
│   │   │   ├── edge
│   │   │   └── pv
│   │   ├── auto
│   │   ├── disc
│   │   ├── id
│   │   ├── metrics
│   │   ├── net-stats
│   │   ├── rm
│   │   ├── sync
│   │   ├── tpd-health
│   │   ├── tpd-stats
│   │   ├── tree
│   │   ├── uptime
│   │   ├── v
│   │   └── viz
│   ├── tps
│   │   ├── add
│   │   ├── list
│   │   └── rm
│   ├── ut
│   │   ├── mdisc
│   │   │   └── graph
│   │   ├── sd
│   │   │   └── graph
│   │   └── tpd
│   │       └── graph
│   ├── util
│   │   ├── edit
│   │   ├── got
│   │   │   ├── dl
│   │   │   ├── head
│   │   │   └── req
│   │   ├── jq
│   │   └── serve
│   ├── visor
│   │   ├── app
│   │   │   ├── arg
│   │   │   │   ├── autostart
│   │   │   │   ├── killswitch
│   │   │   │   ├── netifc
│   │   │   │   ├── passcode
│   │   │   │   └── secure
│   │   │   ├── deregister
│   │   │   ├── log
│   │   │   ├── ls
│   │   │   ├── register
│   │   │   ├── start
│   │   │   └── stop
│   │   ├── dmsg-servers
│   │   ├── go
│   │   ├── halt
│   │   ├── hv
│   │   │   ├── cpk
│   │   │   ├── disable
│   │   │   ├── enable
│   │   │   ├── pk
│   │   │   ├── status
│   │   │   └── ui
│   │   ├── info
│   │   ├── ip
│   │   ├── log
│   │   ├── ping
│   │   │   ├── bandwidth
│   │   │   ├── graph
│   │   │   ├── stop-all
│   │   │   ├── test
│   │   │   ├── tree
│   │   │   └── tree2
│   │   ├── pk
│   │   ├── ports
│   │   ├── proxies
│   │   │   ├── set
│   │   │   └── upstream
│   │   ├── ready
│   │   ├── reinit
│   │   ├── reward
│   │   ├── start
│   │   ├── user
│   │   └── ver
│   └── vpn
│       ├── list
│       ├── server
│       │   ├── start
│       │   ├── status
│       │   └── stop
│       ├── start
│       ├── status
│       ├── stop
│       ├── ui
│       └── url
├── completion
│   ├── bash
│   ├── fish
│   ├── powershell
│   └── zsh
├── cxo
│   ├── cli
│   │   ├── connection
│   │   │   ├── list
│   │   │   └── list-by-feed
│   │   ├── feed
│   │   │   ├── is-sharing
│   │   │   ├── list
│   │   │   ├── share
│   │   │   └── unshare
│   │   ├── kv
│   │   │   ├── create
│   │   │   ├── delete
│   │   │   ├── get
│   │   │   ├── list
│   │   │   └── put
│   │   ├── root
│   │   │   ├── info
│   │   │   ├── last
│   │   │   └── tree
│   │   ├── stat
│   │   ├── stop
│   │   ├── tcp
│   │   │   ├── address
│   │   │   ├── connect
│   │   │   ├── disconnect
│   │   │   ├── subscribe
│   │   │   └── unsubscribe
│   │   └── udp
│   │       ├── address
│   │       ├── connect
│   │       ├── disconnect
│   │       ├── subscribe
│   │       └── unsubscribe
│   └── daemon
├── dmsg
│   ├── conf
│   │   ├── gen-keys
│   │   └── verify-keys
│   ├── curl
│   ├── disc
│   ├── http
│   ├── ip
│   ├── pty
│   │   ├── cli
│   │   │   ├── whitelist
│   │   │   ├── whitelist-add
│   │   │   └── whitelist-remove
│   │   ├── host
│   │   │   └── confgen
│   │   └── ui
│   ├── self-ping
│   ├── server
│   │   ├── config
│   │   │   └── gen
│   │   ├── dial
│   │   └── start
│   ├── socks
│   │   ├── client
│   │   └── server
│   └── web
│       └── srv
├── skycoin
│   ├── cli
│   │   ├── addPrivateKey
│   │   ├── addressBalance
│   │   ├── addressGen
│   │   ├── addressOutputs
│   │   ├── addressTransactions
│   │   ├── addresscount
│   │   ├── blocks
│   │   ├── broadcastTransaction
│   │   ├── checkDBDecoding
│   │   ├── checkdb
│   │   ├── createRawTransaction
│   │   ├── createRawTransactionV2
│   │   ├── decodeRawTransaction
│   │   ├── decryptWallet
│   │   ├── distributeGenesis
│   │   ├── encodeJsonTransaction
│   │   ├── encryptWallet
│   │   ├── fiberAddressGen
│   │   ├── halt
│   │   ├── lastBlocks
│   │   ├── listAddresses
│   │   ├── listWallets
│   │   ├── nextAddress
│   │   ├── pendingTransactions
│   │   ├── richlist
│   │   ├── send
│   │   ├── showConfig
│   │   ├── showSeed
│   │   ├── signTransaction
│   │   ├── status
│   │   ├── transaction
│   │   ├── unusedAddresses
│   │   ├── verifyAddress
│   │   ├── verifyTransaction
│   │   ├── verifyXpub
│   │   ├── version
│   │   ├── walletAddAddresses
│   │   ├── walletBalance
│   │   ├── walletCreate
│   │   ├── walletCreateTemp
│   │   ├── walletHistory
│   │   ├── walletKeyExport
│   │   ├── walletOutputs
│   │   └── walletScanAddresses
│   ├── daemon
│   ├── explorer
│   ├── newcoin
│   │   ├── config
│   │   ├── createcoin
│   │   └── templates
│   └── web
├── svc
│   ├── ar
│   ├── conf
│   │   ├── dmsghttp
│   │   └── http
│   ├── confbs
│   ├── ip
│   ├── nm
│   │   └── deregister
│   ├── rf
│   ├── sd
│   ├── se
│   │   ├── dmsg
│   │   ├── setup
│   │   └── visor
│   ├── sn
│   │   └── health
│   ├── stun
│   ├── tpd
│   ├── tps
│   │   ├── add
│   │   ├── list
│   │   └── rm
│   └── ut
└── visor
```

## Command Reference

# skywire

# skywire

```
┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
└─┐├┴┐└┬┘││││├┬┘├┤ 
└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘
v1.3.46-0.20260418190307-6747d8d73adf
built with go1.26.1 on 2026-04-18T19:03:07Z

Usage:
  skywire

Available Commands:
  app                     skywire native applications
  cli                     Command Line Interface for skywire
  cxo                     CXO object distribution system
  dmsg                    DMSG services & utilities
  skycoin                 skycoin daemon & cli
  svc                     Skywire services
  visor                   Skywire Visor

Flags:
  -b, --bv     print runtime/debug.BuildInfo.Main.Version
  -d, --info   print runtime/debug.BuildInfo
```

## skywire app

```
┌─┐┌─┐┌─┐┌─┐
├─┤├─┘├─┘└─┐
┴ ┴┴  ┴  └─┘

Usage:
  skywire app

Available Commands:
  skychat                     skywire chat application
  skynet-client               skywire port forwarding client application
  skynet-srv                  skywire port forwarding server application
  skysocks                    skywire socks5 proxy server application
  skysocks-client             skywire socks5 proxy client application
  vpn-client                  skywire vpn client application
  vpn-server                  skywire vpn server application
```

### skywire app skychat

```
┌─┐┬┌─┬ ┬┌─┐┬ ┬┌─┐┌┬┐
└─┐├┴┐└┬┘│  ├─┤├─┤ │ 
└─┘┴ ┴ ┴ └─┘┴ ┴┴ ┴ ┴

Usage:
  skywire app skychat



Flags:
      --addr string                 address to bind, put an * before the port if you want to be able to access outside localhost (default ":8001")
      --dmsg                        listen on dmsg network (default true)
      --persist                     persist chat history to a local BoltDB (off by default)
      --persist-db string           path to the BoltDB file (default: <work-dir>/skychat-history.db)
      --persist-max-size int        maximum persisted message size in bytes (default 4096)
      --persist-per-peer-cap int    maximum persisted messages per peer (FIFO eviction) (default 500)
      --persist-per-peer-rate int   persisted messages per minute per peer (rate limit) (default 20)
      --persist-seed int            number of recent messages to seed new SSE clients with (0 disables) (default 50)
      --persist-total-cap int       total persisted storage cap in MB (default 10)
      --persist-ttl int             days to keep persisted messages before sweep (0 disables) (default 30)
      --persist-whitelist string    path to file with one peer PK per line; if set, only these peers are persisted
      --port uint16                 routing port for communication between app and visor
      --skynet                      listen on skynet network (default true)
```

### skywire app skynet-client

```
Skynet client connects to a remote skynet server and forwards traffic to localhost

Usage:
  skywire app skynet-client



Flags:
      --local int     local port to listen on
      --port uint16   routing port for communication between app and visor
      --raw-tcp       use raw TCP forwarding instead of HTTP
      --remote int    remote port to forward
      --srv string    remote server public key
```

### skywire app skynet-srv

```
Skynet exposes local TCP ports over skywire network via the built-in sky_forwarding service

Usage:
  skywire app skynet-srv



Flags:
      --ports string       comma-separated list of local ports to expose (e.g., '8080,9000')
      --rpc string         visor RPC address (default "localhost:3435")
      --whitelist string   comma-separated list of public keys allowed to connect (not currently supported)
```

### skywire app skysocks

```
┌─┐┬┌─┬ ┬┌─┐┌─┐┌─┐┬┌─┌─┐
└─┐├┴┐└┬┘└─┐│ ││  ├┴┐└─┐
└─┘┴ ┴ ┴ └─┘└─┘└─┘┴ ┴└─┘

Usage:
  skywire app skysocks



Flags:
      --port uint16        routing port for communication between app and visor
      --whitelist string   comma-separated list of public keys allowed to connect (empty = allow all)
```

### skywire app skysocks-client

```
┌─┐┬┌─┬ ┬┌─┐┌─┐┌─┐┬┌─┌─┐   ┌─┐┬  ┬┌─┐┌┐┌┌┬┐
└─┐├┴┐└┬┘└─┐│ ││  ├┴┐└─┐───│  │  │├┤ │││ │ 
└─┘┴ ┴ ┴ └─┘└─┘└─┘┴ ┴└─┘   └─┘┴─┘┴└─┘┘└┘ ┴

Usage:
  skywire app skysocks-client



Flags:
      --addr string      Client address to listen on (default ":1080")
      --http string      http proxy mode
      --port uint16      routing port for communication between app and visor
      --retry-time int   delay between each try (default 5)
      --srv string       PubKey of the server to connect to
      --tries int        number of tries (default 3)
```

### skywire app vpn-client

```
┬  ┬┌─┐┌┐┌   ┌─┐┬  ┬┌─┐┌┐┌┌┬┐
└┐┌┘├─┘│││───│  │  │├┤ │││ │ 
 └┘ ┴  ┘└┘   └─┘┴─┘┴└─┘┘└┘ ┴

Usage:
  skywire app vpn-client



Flags:
      --dns string    address of DNS want set to tun
      --killswitch    If set, the Internet won't be restored during reconnection attempts
      --pk string     local pubkey
      --port uint16   routing port for communication between app and visor
      --sk string     local seckey
      --srv string    PubKey of the server to connect to
```

### skywire app vpn-server

```
┬  ┬┌─┐┌┐┌   ┌─┐┌─┐┬─┐┬  ┬┌─┐┬─┐
└┐┌┘├─┘│││───└─┐├┤ ├┬┘└┐┌┘├┤ ├┬┘
 └┘ ┴  ┘└┘   └─┘└─┘┴└─ └┘ └─┘┴└─

Usage:
  skywire app vpn-server



Flags:
      --netifc string      Default network interface for multiple available interfaces
      --pk string          local pubkey
      --port uint16        routing port for communication between app and visor
      --secure             Forbid connections from clients to server local network (default true)
      --sk string          local seckey
      --whitelist string   comma-separated list of public keys allowed to connect (empty = allow all)
```

## skywire cli

```
┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐   ┌─┐┬  ┬
└─┐├┴┐└┬┘││││├┬┘├┤ ───│  │  │
└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘   └─┘┴─┘┴

Usage:
  skywire cli


Configuration:
  config                  Generate or update a skywire config

Visor:
  gotop                   Terminal based graphical activity monitor
  visor                   Query the Skywire Visor

Apps:
  proxy                   Skysocks client
  skychat                 Skychat messaging
  skynet                  Skynet port forwarding
  vpn                     VPN client

Networking:
  dmsg                    Dmsg utilities
  rg                      Route group management
  route                   View and set rules
  tp                      View and manage transports
  tps                     Control embedded Transport Setup Node

Discovery & lookups:
  mdisc                   Query DMSG Discovery
  pv                      Public Visors
  sd                      Service discovery network statistics
  svc                     Query skywire deployment services
  ut                      query uptime tracker

Rewards:
  log                     survey & transport log collection
  reward                  skycoin reward address or xpub key
  rewards                 calculate rewards from uptime data & collected surveys
  survey                  system survey

Utilities:
  util                    Bundled utility commands

```

### skywire cli completion

```
Generate completion script

Usage:
  skywire cli completion [bash|zsh|fish|powershell]


```

### skywire cli config

```
Generate or update the config file used by skywire-visor.

Usage:
  skywire cli config

Available Commands:
  check-pk                check a skywire public key
  gen                     Generate a config file
  gen-keys                generate public / secret keypair
  parse                   check for errors in parsing skywire config
  pk                      derive public key from a secret key
  show                    Show the running visor's config
  update                  Update a config file
```

#### skywire cli config check-pk

```
check a skywire public key

Usage:
  skywire cli config check-pk <public-key>


```

#### skywire cli config gen

```
Generate a config file

	Config defaults file may also be specified with:
	SKYENV=/path/to/skywire.conf skywire-cli config gen
	print the SKYENV file template with:
	skywire-cli config gen -q

Usage:
  skywire cli config gen



Flags:
  -o, --out string          output config: skywire-config.json
  -q, --envs                show the conf template (reflects flags passed)
  -Q, --envout string       write conf template to file (reflects flags passed)
  -r, --regen               re-generate existing config & retain keys
      --http                use only http connection to skywire services (no dmsg)
  -b, --bestproto           best protocol (dmsg | direct) based on location
  -y, --autoconn            disable autoconnect to public visors
  -i, --ishv                local hypervisor configuration
  -j, --hvpks string        list of public keys to add as hypervisor
      --dmsgpty string      add dmsgpty whitelist PKs
      --vpnwl string        comma-separated list of public keys allowed to connect to vpn server (empty = allow all)
      --serveproxy          autostart proxy server (default: true) (default true)
      --proxywl string      comma-separated list of public keys allowed to connect to proxy server (empty = allow all)
      --servechat           autostart skychat (default: true) (default true)
      --rewardaddr string   skycoin reward address or xpub key
  -p, --pkg                 use path for package: /opt/skywire
  -u, --user                use paths for user space: /home/d0mo
      --all                 show all flags
```

#### skywire cli config gen-keys

```
generate public / secret keypair

Usage:
  skywire cli config gen-keys


```

#### skywire cli config parse

```
check for errors in parsing skywire config

Usage:
  skywire cli config parse <skywire-config.json>


```

#### skywire cli config pk

```
derive public key from a secret key

Usage:
  skywire cli config pk <secret-key-hex>


```

#### skywire cli config show

```
Print the configuration currently in use by the running visor.
Use --path to print just the config file path.
Use --json for machine-readable output.

Usage:
  skywire cli config show



Flags:
      --path         print only the config file path
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli config update

```
Update a config file

Usage:
  skywire cli config update

Available Commands:
  hv                      update hypervisor config
  sc                      update skysocks-client config
  ss                      update skysocks-server config
  svc                     update services-config.json file from config bootstrap service
  vpnc                    update vpn-client config
  vpns                    update vpn-server config

Flags:
  -a, --endpoints                update server endpoints
      --log-level string         level of logging in config
  -b, --url string               service config URL (default "http://conf.skywire.skycoin.com")
  -t, --testenv                  use test deployment
      --public-autoconn string   change public autoconnect configuration
      --set-minhop int           change min hops value (default -1)
  -i, --input string             path of input config file.
  -o, --output string            config file to output
  -u, --user                     update config at: $HOME/skywire-config.json
```

##### skywire cli config update hv

```
update hypervisor config

Usage:
  skywire cli config update hv



Flags:
  -+, --add-pks string   public keys of hypervisors that should be added to this visor
  -r, --reset            resets hypervisor configuration

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -u, --user            update config at: $HOME/skywire-config.json
```

##### skywire cli config update sc

```
update skysocks-client config

Usage:
  skywire cli config update sc



Flags:
  -+, --add-server string   add skysocks server address to skysock-client
  -r, --reset               reset skysocks-client configuration

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -u, --user            update config at: $HOME/skywire-config.json
```

##### skywire cli config update ss

```
update skysocks-server config

Usage:
  skywire cli config update ss



Flags:
  -w, --whitelist string   comma-separated public keys allowed to connect (empty = allow all)
  -r, --reset              reset skysocks configuration

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -u, --user            update config at: $HOME/skywire-config.json
```

##### skywire cli config update svc

```
update services-config.json file from config bootstrap service

Usage:
  skywire cli config update svc



Flags:
  -p, --path string   path of services-config file, default is for pkg installation (default "/opt/skywire/services-config.json")
      --rpc string    RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
      --no-rpc        skip visor RPC (DmsgHTTP) step
      --no-dmsg       skip direct DMSG HTTP step
      --no-http       skip direct HTTP fallback step

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -u, --user            update config at: $HOME/skywire-config.json
```

##### skywire cli config update vpnc

```
update vpn-client config

Usage:
  skywire cli config update vpnc



Flags:
  -x, --killsw string       change killswitch status of vpn-client
      --add-server string   add server address to vpn-client
  -r, --reset               reset vpn-client configurations

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -u, --user            update config at: $HOME/skywire-config.json
```

##### skywire cli config update vpns

```
update vpn-server config

Usage:
  skywire cli config update vpns



Flags:
  -w, --whitelist string   comma-separated public keys allowed to connect (empty = allow all)
      --secure string      change secure mode status of vpn-server
      --autostart string   change autostart of vpn-server
      --netifc string      set default network interface
  -r, --reset              reset vpn-server configurations

Global Flags:
  -i, --input string    path of input config file.
  -o, --output string   config file to output
  -u, --user            update config at: $HOME/skywire-config.json
```

### skywire cli dmsg

```
Commands that use DMSG for communication

Usage:
  skywire cli dmsg

Available Commands:
  connect-all              Open a dmsg session to every known server
  curl                     Fetch data over dmsg
  probe                    Probe a remote visor's dmsg port reachability
  pty                      Interact with remote visors
  sessions                 List dmsg servers each visor dmsg client is connected to
  set-sessions             Persist dmsg.sessions_count and connect-all immediately
```

#### skywire cli dmsg connect-all

```
Enumerates every dmsg server in discovery and ensures the visor's
dmsg client has an active session to each one.

This is a one-shot action — it does not persist to the visor config
and does not change sessions_count. Once a session dies, the normal
reconnect behavior (which respects sessions_count) applies. For
persistent "connect to all" behavior use:
  skywire cli dmsg set-sessions -n 0

Useful for visors that host the embedded route-setup-node or
transport-setup-node and need to reach arbitrary destinations without
waiting on phase-3 new-session dials during route setup.

Usage:
  skywire cli dmsg connect-all



Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
      --json         print output in json
```

#### skywire cli dmsg curl

```
DMSG curl - fetch data over dmsg network.

By default uses the local visor's dmsg client via RPC.
Use --sk flag to start a standalone dmsg client instead.

Example URLs:
  dmsg://<public-key>:<port>/path
  dmsg://<public-key>/path  (port defaults to 80)

Usage:
  skywire cli dmsg curl <dmsg-url>



Flags:
      --rpc string         RPC server address (default "localhost:3435")
  -l, --loglvl string      [ debug | warn | error | fatal | panic | trace | info ] (default "fatal")
  -s, --sk cipher.SecKey   use a custom secret key (starts new dmsg client instead of using visor's) (default 0000000000000000000000000000000000000000000000000000000000000000)
  -d, --data string        HTTP POST data
  -o, --out string         output filepath
  -r, --replace            replace existing output file
  -t, --try int            download attempts (0 unlimits) (default 1)
  -w, --wait int           time to wait between attempts (seconds)
  -a, --agent string       HTTP user agent (default "skywire-cli/v1.3.46-0")
```

#### skywire cli dmsg probe

```
Probe a remote visor on a specific dmsg port via dmsg.

The probe performs a full DialStream (noise handshake) through the dmsg server
bridge to the destination. If a listener is active on the specified port, the
handshake completes and the probe reports success. If nothing is listening,
the probe reports failure.

By default, the probe uses the local visor's dmsg client via RPC. Use -s to
bootstrap a standalone dmsg client (no running visor required).

Common ports:
  80   - dmsghttp log server (/health, /ping)
  136  - route setup await port (used by RSN for route establishment)
  22   - dmsgpty (remote terminal)
  7    - dmsg ctrl
  8    - dmsg ping

Examples:
  skywire cli dmsg probe <pk> 136        # via visor RPC
  skywire cli dmsg probe -s <pk> 136     # standalone (no visor needed)
  skywire cli dmsg probe -s <pk> 80      # check if log server is up

Usage:
  skywire cli dmsg probe <public-key> <port>



Flags:
  -s, --standalone   use a standalone dmsg client (no running visor needed)
```

#### skywire cli dmsg pty

```
Commands for interacting with remote visors via dmsgpty

Usage:
  skywire cli dmsg pty

Available Commands:
  list                    List connected visors
  start                   Start dmsgpty session
  ui                      Open dmsgpty UI in default browser
  url                     Show dmsgpty UI URL
```

##### skywire cli dmsg pty list

```
List connected visors

Usage:
  skywire cli dmsg pty list



Flags:
      --rpc string   RPC server address (default "localhost:3435")
```

##### skywire cli dmsg pty start

```
Start dmsgpty session

Usage:
  skywire cli dmsg pty start <pk>



Flags:
  -p, --port string   port of remote visor dmsgpty (default "22")
      --rpc string    RPC server address (default "localhost:3435")
```

##### skywire cli dmsg pty ui

```
Open dmsgpty UI in default browser

Usage:
  skywire cli dmsg pty ui



Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to
```

##### skywire cli dmsg pty url

```
Show dmsgpty UI URL

Usage:
  skywire cli dmsg pty url



Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to
```

#### skywire cli dmsg sessions

```
Shows every dmsg client running inside the visor and the set of
dmsg servers each one currently has an active session with.

A visor typically runs THREE separate dmsg clients, each with its
own public key and its own independent session set:

  main             — the visor's primary dmsg client (visor PK)
  route_setup     — the embedded Route Setup Node (route_setup_pk)
  transport_setup — the embedded Transport Setup Node (transport_setup_pk)

Checking 'skywire cli visor info' only shows the main client. Use
this command to verify that the RSN and TPS clients are reaching the
same servers — if they're not, route/transport setup will fail even
when the main visor is healthy.

Usage:
  skywire cli dmsg sessions



Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
      --json         print output in json
```

#### skywire cli dmsg set-sessions

```
Updates the dmsg.sessions_count setting in the visor's config
file (so it survives restart) and immediately triggers a connect-all
action so the running dmsg client reaches the new session target
without needing a restart.

Note: the live dmsg client's internal MinSessions is not currently
mutated at runtime — the persisted value takes full effect only on
the next visor restart. The connect-all one-shot supplements this by
opening any sessions that are currently missing. Once the visor
restarts, the reconnect loop will maintain the persisted count on an
ongoing basis.

A value of 0 means "connect to all available servers and keep
reconnecting to any that drop" — recommended for RSN / TPS visors.

Usage:
  skywire cli dmsg set-sessions



Flags:
  -n, --count int    sessions_count to persist; 0 = connect to all available servers
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
      --json         print output in json
```

### skywire cli gotop

```
A terminal based graphical activity monitor inspired by gtop and vtop.

By default, tries to connect to the local visor's gRPC server for system stats.
If unavailable, falls back to running gotop directly using local gopsutil.

Usage:
  skywire cli gotop



Flags:
  -a, --averagecpu       show average CPU usage
  -c, --color string     color scheme (default "default")
  -x, --export string    export metrics on port (e.g., :8080)
      --fahrenheit       use fahrenheit for temperature
  -l, --layout string    layout: default, minimal, battery, procs, kitchensink (default "default")
      --local            run gotop directly using local gopsutil (skip visor gRPC)
      --mbps             show network in Mbps
      --once             show single snapshot and exit (text mode, remote only)
  -p, --percpu           show per-cpu usage
  -n, --proc-limit int   number of processes to show in remote mode (default 10)
      --procs            include processes in remote stats (default true)
  -r, --rate string      update interval (default "1s")
      --remote string    connect to remote visor by public key (uses local visor's DMSG)
  -s, --statusbar        show status bar
```

### skywire cli log

```
Fetch health, survey, and transport logging from visors which are online in the uptime tracker
http://ut.skywire.skycoin.com/uptimes?v=v2
http://ut.skywire.skycoin.com/uptimes?v=v2&visors=<pk1>;<pk2>;<pk3>

Usage:
  skywire cli log

Available Commands:
  st                      survey tree
  tp                      display collected transport bandwidth logging

Flags:
  -l, --log                       fetch only transport logs
  -v, --survey                    fetch only surveys
  -f, --file string               fetch only a specific file from all online visors
  -k, --pks string                fetch only from specific public keys ; semicolon separated
  -d, --dir string                save files to specified dir (default "log_collecting")
  -c, --clean                     delete files and folders on errors
      --minv string               minimum visor version to fetch from (default "v1.3.19")
      --include-versions string   list of version that not satisfy our minimum version condition, but we want include them
  -n, --duration int              number of days before today to fetch transport logs for
      --all                       consider all visors ; no version filtering
      --batchSize int             number of visor in each batch (default 50)
      --maxfilesize int           maximum file size allowed to download during collecting logs, in KB (default 1024)
  -D, --dmsg-disc string          dmsg discovery url
                                   (default "http://dmsgd.skywire.skycoin.com")
  -u, --ut string                 uptime tracker url
                                   (default "http://ut.skywire.skycoin.com")
  -s, --sk cipher.SecKey          a random key is generated if unspecified
                                   (default 0000000000000000000000000000000000000000000000000000000000000000)
      --cleanup                   run cleanup after collection (remove old/invalid files) (default true)
      --backup-dir string         backup directory to also clean (default "log_backups")
      --max-age int               maximum age in days for files before deletion (default 7)
      --rpc string                RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli log st

```
survey tree

Usage:
  skywire cli log st



Flags:
  -d, --lcdir string   path to surveys & transport bandwidth logging 
  -r, --noerr          hide error logging from output
  -p, --pk string      public key(s) to check ; comma separated
  -u, --ut             show uptime percentage for the past two days and current online status
```

#### skywire cli log tp

```
display collected transport bandwidth logging

Usage:
  skywire cli log tp



Flags:
  -d, --dir string   path to surveys & transport bandwidth logging
```

### skywire cli mdisc

```
Query DMSG Discovery
	list entries in dmsg discovery

Use --testenv or SKYWIRETEST=1 to use test deployment services.

Usage:
  skywire cli mdisc

Available Commands:
  entry                   Fetch an entry
  servers                 Fetch available servers

Flags:
      --cdd string   DMSG cache dir ("" to disable) (default "/tmp/dmsgd.skywire.skycoin.com")
  -m, --cfa int      update cache file if older than n minutes (default 5)
  -s, --stats        count the number of results
      --testenv      use test deployment
      --url string   specify alternative DMSG discovery url (default "http://dmsgd.skywire.skycoin.com")
```

#### skywire cli mdisc entry

```
Fetch an entry

Usage:
  skywire cli mdisc entry <visor-public-key>



Flags:
      --url string   specify alternative DMSG discovery url (default "http://dmsgd.skywire.skycoin.com")
```

#### skywire cli mdisc servers

```
Fetch available servers

Usage:
  skywire cli mdisc servers



Flags:
      --url string   specify alternative DMSG discovery url (default "http://dmsgd.skywire.skycoin.com")
```

### skywire cli proxy

```
Skysocks client

Usage:
  skywire cli proxy

Available Commands:
  list                    List servers
  route                   Manage routes for the active proxy connection
  server                  Skysocks server (SOCKS5 proxy server)
  start                   start the proxy client
  status                  proxy client status
  stop                    stop the proxy client
  test                    Test proxy servers from service discovery

Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli proxy list

```
List proxy servers from service discovery
http://sd.skycoin.com/api/services?type=proxy
http://sd.skycoin.com/api/services?type=proxy&country=US

Set cache dir to "" to avoid using cache files
default virtual port of servers: 3

Use --testenv or SKYWIRETEST=1 to use test deployment services.

Usage:
  skywire cli proxy list



Flags:
      --cds string       SD cache dir ("" to disable) (default "/tmp/sd.skycoin.com")
      --cdu string       UT cache dir ("" to disable) (default "/tmp/ut.skywire.skycoin.com")
  -m, --cfa int          update cache files if older than n minutes (default 5)
  -c, --country string   filter by country code
      --json             print output in json
      --maxv string      filter by maximum version (<=)
      --minv string      filter by minimum version (>=)
  -o, --noton            do not filter by online status in UT
      --offline          show only offline servers (red)
  -k, --pk string        check proxy service discovery for public key
  -r, --raw              print raw json data
  -a, --sdurl string     service discovery url (default "http://sd.skycoin.com")
  -s, --stats            return only a count of the results
      --testenv          use test deployment
  -w, --uturl string     uptime tracker url (default "http://ut.skywire.skycoin.com")
  -v, --version string   filter by version

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli proxy route

```
Manage routes for the active proxy connection

Usage:
  skywire cli proxy route

Available Commands:
  add                     Add a mux route to the active proxy connection
  remove                  Remove a mux route from the active proxy connection

Flags:
      --name string   name of the proxy client app (default "skysocks-client")

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli proxy route add

```
Add an additional multiplexed route to the running proxy's connection.
The route will be established through the routing system using the specified
transport as the first hop. Traffic is distributed across all routes
according to the current mux mode (auto or equal).

Usage:
  skywire cli proxy route add <transport-id>



Global Flags:
      --name string   name of the proxy client app (default "skysocks-client")
      --rpc string    RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli proxy route remove

```
Remove a specific transport's route from the proxy's multiplexed connection.
Traffic is redistributed across remaining routes. Cannot remove the last route.

Usage:
  skywire cli proxy route remove <transport-id>



Global Flags:
      --name string   name of the proxy client app (default "skysocks-client")
      --rpc string    RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli proxy server

```
Control the skysocks server application.

The skysocks server provides a SOCKS5 proxy over the Skywire network.
Other visors can connect to this proxy using skysocks-client.

Usage:
  skywire cli proxy server

Available Commands:
  start                   Start the skysocks server
  status                  Show skysocks server status
  stop                    Stop the skysocks server

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli proxy server start

```
Start the skysocks server

Usage:
  skywire cli proxy server start



Flags:
      --external           force external launcher
      --internal           force internal launcher
      --port uint16        routing port for communication between app and visor
  -w, --whitelist string   comma-separated list of public keys allowed to connect (empty = allow all)

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli proxy server status

```
Show skysocks server status

Usage:
  skywire cli proxy server status



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli proxy server stop

```
Stop the skysocks server

Usage:
  skywire cli proxy server stop



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli proxy start

```
start the proxy client

Usage:
  skywire cli proxy start



Flags:
  -a, --addr string       address of proxy for use (default ":1080")
      --existing-tp       only use existing transports, don't create new ones
      --external          force external launcher
      --http string       address for http proxy
      --internal          force internal launcher
      --local-route       calculate routes locally instead of using route finder
      --mux int           number of parallel mux routes (0=disabled, 2+=enabled)
      --mux-mode string   mux weight distribution mode: auto (latency-based) or equal (round-robin) (default "auto")
  -n, --name string       name of skysocks client
  -k, --pk string         server public key
      --port uint16       routing port for communication between proxy (skysocks) and visor
  -t, --timeout int       timeout for starting proxy

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli proxy status

```
proxy client status

Usage:
  skywire cli proxy status



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli proxy stop

```
stop the proxy client

Usage:
  skywire cli proxy stop



Flags:
      --all           stop all skysocks client
      --name string   specific skysocks client that want stop

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli proxy test

```
Fetch proxy servers from service discovery and test connectivity.
For each proxy, check if visor has a transport to it, then attempt to
make an HTTP request through the proxy to verify it's working.

With --connect flag, connects to all online proxies (adds transports)
without HTTP testing. Use --version to filter by visor version.

Results show which proxies are reachable and their response latency.

Use --testenv or SKYWIRETEST=1 to use test deployment services.

Usage:
  skywire cli proxy test



Flags:
  -b, --batch int        number of proxies to test (1=sequential/stable, >1=parallel/experimental) (default 1)
      --cds string       SD cache dir ("" to disable) (default "/tmp/sd.skycoin.com")
      --cdu string       UT cache dir ("" to disable) (default "/tmp/ut.skywire.skycoin.com")
  -m, --cfa int          update cache files if older than n minutes (default 5)
  -c, --connect          connect only mode: add transports without HTTP testing
  -k, --country string   filter proxies by country code
      --delay int        delay in ms between dispatching tests (0=auto: 500ms for batch>1, 0 for batch=1)
      --existing-tp      only use existing transports, don't create new ones
      --local-route      calculate routes locally instead of using route finder
  -s, --pk string        test a specific proxy server by public key
  -a, --sdurl string     service discovery url (default "http://sd.skycoin.com")
      --testenv          use test deployment
  -t, --timeout int      timeout in seconds for HTTP request (route setup has separate 30s timeout) (default 10)
  -p, --transport        only test proxies that have an existing transport
  -u, --url string       URL to fetch through proxy for testing (default "http://ip.skycoin.com")
  -w, --uturl string     uptime tracker url (default "http://ut.skywire.skycoin.com")
  -v, --verbose          verbose output
  -V, --version string   filter proxies by version (empty to skip)
      --via string       test 2-hop routes via specified visor (queries TPD for its transports)

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli pv

```
List public visors from service discovery
http://sd.skycoin.com/api/services?type=visor

Returns only public keys, one per line.
Use -t to show transport counts per visor.

Cache files are stored in directories named after service hosts.
Set cache dir to "" to disable caching for that service.

Use --testenv or SKYWIRETEST=1 to use test deployment services.

Usage:
  skywire cli pv



Flags:
      --cds string       SD cache dir ("" to disable) (default "/tmp/sd.skycoin.com")
      --cdt string       TPD cache dir ("" to disable) (default "/tmp/tpd.skywire.skycoin.com")
      --cdu string       UT cache dir ("" to disable) (default "/tmp/ut.skywire.skycoin.com")
  -m, --cfa int          update cache files if older than n minutes (default 5)
  -c, --country string   filter by country code
  -n, --min int          minimum transport count (requires -t)
      --no-dmsg          skip direct DMSG HTTP step
      --no-http          skip direct HTTP fallback step
      --no-rpc           skip visor RPC (DmsgHTTP) step
  -o, --noton            do not filter by online status in UT
  -r, --raw              print raw json data
      --rpc string       RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
  -a, --sdurl string     service discovery url (default "http://sd.skycoin.com")
  -s, --stats            return only a count of the results
      --testenv          use test deployment
  -d, --tpdurl string    transport discovery url (default "http://tpd.skywire.skycoin.com")
  -t, --transports       show transport count per visor
  -w, --uturl string     uptime tracker url (default "http://ut.skywire.skycoin.com")
  -v, --version string   filter by version
```

### skywire cli reward

```

    skycoin reward address set to:
    xpub6DH2sNie8sDh5XJm1UCTW412yEAJAMZod8bAfFMrX2vu5VMFqjig1tLoKYhWCrz8Sp26mFtydunEhAk3Mu1JAuYzY3ESsnW9BAqKFxfh1Wh

Usage:
  skywire cli reward <address | xpub> || [flags]

Available Commands:
  rules                   display the mainnet rules

Flags:
      --all   show all flags
```

#### skywire cli reward rules

```
display the mainnet rules

Usage:
  skywire cli reward rules



Flags:
  -l, --html   render html from markdown
  -r, --raw    print raw the embedded file
```

### skywire cli rewards

```

Collect surveys:  skywire-cli log
Fetch uptimes:    skywire-cli ut > ut.txt

Process rewards:  skywire-cli rewards --process

Architectures:
[amd64 arm64 386 arm ppc64 riscv64 wasm loong64 mips mips64 mips64le mipsle ppc64le s390x null all]

Usage:
  skywire cli rewards

Available Commands:
  bot                     reward notification telegram bot
  bw-collect              collect bandwidth data from TPD for reward calculation
  loginchain              start the login chain nodes (block publisher + peer)
  script                  print reward system scripts
  svc                     verify services in survey
  systemd                 set up systemd services for reward system
  tp-collect              collect transport data and track visors with sufficient transports
  ui                      reward system UI server

Flags:
  -s, --loglvl string    [ debug | warn | error | fatal | panic | trace ] (default "info")
  -d, --date string      date for which to calculate reward (default "2026-04-17")
  -k, --pk string        check reward for pubkey
  -n, --noarch strings   disallowed architectures, comma separated (default [null,wasm])
  -w, --a1 strings       pool 1 allowed arch, comma separated (default [arm64,arm,ppc64,riscv64,loong64,mips,mips64,mips64le,mipsle,ppc64le,s390x])
  -x, --a2 strings       pool 2 allowed arch, comma separated (default [amd64,386])
  -y, --year int         yearly total rewards per pool (default 408000)
  -u, --utfile string    uptime tracker data file (default "ut.txt")
  -p, --lpath string     path to the surveys (default "log_collecting")
  -0, --h0               hide statistical data
  -1, --h1               hide survey csv data
  -2, --h2               hide reward csv data
  -e, --err              account for non rewarded keys
  -r, --process          run complete reward processing workflow
  -t, --require-tp       require minimum transports (from hist/YYYY-MM-DD_transports.txt) (default true)
  -T, --tp-hist string   path to transport history directory (default "hist")
  -b, --require-bw       require minimum bandwidth (proportional reward based on bandwidth)
  -B, --min-bw uint      minimum bandwidth in bytes to qualify (used with --require-bw) (default 64)
  -S, --sat-exp float    regional saturation exponent (1.0=no derating, 0.5=sqrt, 0=all countries equal) (default 0.5)
```

#### skywire cli rewards bot

```
reward notification telegram bot

Usage:
  skywire cli rewards bot



Flags:
  -w, --watch string   File to watch - file where reward transaction IDs are recorded (default "../reward/rewards/transactions0.txt")
```

#### skywire cli rewards bw-collect

```
Fetches per-visor bandwidth data from TPD and records daily bandwidth.

This command:
1. Fetches all transport metrics from TPD /metrics?days=1&bandwidth=true&edges=true
2. Builds a PK→IP map from hardware surveys to detect same-LAN transports
3. Excludes bandwidth from transports where both edges share the same external IP
4. Aggregates remaining bandwidth per visor
5. Writes hist/YYYY-MM-DD_bandwidth.json as map[string]uint64 (pk → daily bytes)
6. Caches results in /tmp/tpd_bandwidth.json with 5-min TTL

Visors below the minimum bandwidth threshold are excluded.
Designed to be run hourly by the reward service.

Usage:
  skywire cli rewards bw-collect



Flags:
  -s, --loglvl string   [ debug | warn | error | fatal | panic | trace ] (default "info")
  -n, --no-cache        bypass cache and fetch fresh data
  -p, --hist string     path to history directory for daily files (default "hist")
  -b, --min-bw uint     minimum bandwidth in bytes to qualify (default 64)
  -l, --lpath string    path to hardware surveys (for same-LAN detection) (default "log_collecting")
      --rpc string      RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
      --no-rpc          skip visor RPC (DmsgHTTP) step
      --no-dmsg         skip direct DMSG HTTP step
      --no-http         skip direct HTTP fallback step
```

#### skywire cli rewards loginchain

```
Starts a two-node login chain for wallet ownership verification.

Block publisher on :6001 (P2P) / :6421 (API)
Peer node on :6002 (P2P) / :6422 (API) — serves wallet API

The reward system UI server connects to the peer node:
  skywire cli rewards ui --login-node http://127.0.0.1:6422

Blockchain data is wiped on every startup (fresh chain).
The genesis wallet (login_genesis.json) is preserved across restarts.

IMPORTANT: Run this command from the same directory as the reward
server's -W working directory, or use -W to specify it explicitly.
The loginchain reads login_genesis.json and login_fiber.toml from
the working directory. A mismatch causes "no unspents to spend".

LOGIN VERIFICATION FLOW

  1. User submits their reward address (skycoin address or xpub)
  2. Server funds a login address with coins on the login chain
  3. User sends those coins back to the genesis address
  4. Server confirms the transaction, proving wallet ownership

For xpub users, the login address is derived from the change chain
(m/44'/coin'/0'/1/0) to avoid collision with receiving addresses.
This requires the account-level xpub (--path=0), not the external
chain xpub shown in the Skycoin web wallet GUI (--path=0/0).

See 'skywire reward --help' for xpub setup instructions.

Usage:
  skywire cli rewards loginchain



Flags:
  -W, --wd string   working directory for login chain files (login_genesis.json, login_fiber.toml) (default "/home/d0mo/go/src/github.com/0pcom/skywire")
```

#### skywire cli rewards script

```
Print the reward system scripts. Pipe to bash to execute.

Usage:
  skywire cli rewards script

Available Commands:
  getlogs                 getlogs.sh - `skywire cli log` wrapper
  reward                  reward.sh - `skywire cli rewards` wrapper script
```

##### skywire cli rewards script getlogs

```
getlogs.sh - `skywire cli log` wrapper

Usage:
  skywire cli rewards script getlogs



Flags:
  -m, --minv string   minimum version
```

##### skywire cli rewards script reward

```
reward.sh - `skywire cli rewards` wrapper script

Usage:
  skywire cli rewards script reward



Flags:
  -d, --date string   date for which to calculate rewards
```

#### skywire cli rewards svc

```
verify services in survey

Usage:
  skywire cli rewards svc



Flags:
  -s, --loglvl string   [ debug | warn | error | fatal | panic | trace ] (default "info")
  -k, --pk string       verify services in survey for pubkey
  -p, --lpath string    path to the surveys (default "log_collecting")
```

#### skywire cli rewards systemd

```
set up systemd services for reward system
must be run with sufficient permissions to write to output path

Usage:
  skywire cli rewards systemd



Flags:
  -o, --out string      path to output systemd services (default "/etc/systemd/system")
  -p, --path string     reward system data dir path (default "/home/d0mo/go/src/github.com/0pcom/skywire")
  -s, --skyenv string   env config file path (default "fr.conf")
  -u, --user string     user to set - should have write permission on path (default "d0mo")
```

#### skywire cli rewards tp-collect

```
Fetches transport data from TPD and tracks which visors have sufficient transports.

This command:
1. Fetches per-key transport stats from TPD (cached for 5 minutes in /tmp/tpd.json)
2. Identifies visors with at least the minimum required transports (default: 2)
3. Appends qualifying public keys to a daily file in the hist/ directory

The daily file format is: hist/YYYY-MM-DD_transports.txt
Each line contains a public key that had sufficient transports at the time of collection.

This is designed to be run hourly by the reward service.

Usage:
  skywire cli rewards tp-collect



Flags:
  -s, --loglvl string   [ debug | warn | error | fatal | panic | trace ] (default "info")
  -m, --min int         minimum transports required (default 2)
  -a, --all             show all visors with transports (not just those meeting minimum)
  -n, --no-cache        bypass cache and fetch fresh data
  -p, --hist string     path to history directory for daily files (default "hist")
      --rpc string      RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
      --no-rpc          skip visor RPC (DmsgHTTP) step
      --no-dmsg         skip direct DMSG HTTP step
      --no-http         skip direct HTTP fallback step
```

#### skywire cli rewards ui

```
skycoin reward system user interface server and skywire network metrics:
 https://fiber.skywire.dev
┌─┐┬┌┐ ┌─┐┬─┐
├┤ │├┴┐├┤ ├┬┘
└  ┴└─┘└─┘┴└─run the web application

.conf file may also be specified with
SKYENV=/path/to/fiber.conf fiber run

Usage:
  skywire cli rewards ui



Flags:
      --canonical string           canonical domain for SEO (e.g. https://theskywirenetwork.net) (default "https://theskywirenetwork.net")
  -D, --dmsg-disc string           dmsg discovery url (default "http://dmsgd.skywire.skycoin.com")
  -d, --dport uint16               dmsg port to serve (default 80)
  -e, --dsess int                  dmsg sessions (default 1)
  -O, --ensure-online string       Exit when the specified URL cannot be fetched;
                                   i.e. https://fiber.skywire.dev
      --health-only                serve only /health endpoint for testing
      --login-chain-flags string   override flags for login chain skycoin daemon subprocess
                                   (default: --block-publisher --localhost-only --download-peerlist=false
                                   --disable-default-peers --disable-csrf --host-whitelist=fiber.skywire.dev)
      --login-node string          login chain node: empty=disabled, 'auto'=auto-setup on localhost:6421,
                                   or URL of external node (e.g. http://localhost:6421)
  -p, --port uint                  port to serve (default 80)
  -s, --sk cipher.SecKey           a random key is generated if unspecified
                                    (default 0000000000000000000000000000000000000000000000000000000000000000)
      --skycoin-node string        Skycoin mainnet node URL for reward transaction broadcasts (default "http://127.0.0.1:6420")
  -W, --wd string                  location of dir containing 'log_collection' & reward 'hist' dirs (default "/home/d0mo/go/src/github.com/0pcom/skywire")
  -w, --wl string                  add whitelist keys, comma separated to permit POST of reward transaction to be broadcast
```

### skywire cli rg

```
View active route groups, their associated apps, and live traffic stats.

Usage:
  skywire cli rg

Available Commands:
  ls                      List active route groups with app associations and live stats

Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli rg ls

```
List active route groups with app associations and live stats

Usage:
  skywire cli rg ls



Flags:
      --json   output as JSON

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli route

```

    View and set routing rules

Usage:
  skywire cli route

Available Commands:
  add                     Add routing rule
  calc                    Calculate routes locally or control visor's local route calculation
  find                    Query the Route Finder
  groups                  List active route groups
  rm                      Remove routing rule
  rsn-stats               Show embedded Route Setup Node request statistics

Flags:
  -n, --nrid         display the next available route id
  -i, --rid string   show routing rule matching route ID
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli route add

```

    Add routing rule

Usage:
  skywire cli route add ( app | fwd | intfwd )

Available Commands:
  a                       Add app/consume routing rule
  b                       Add intermediary forward routing rule
  c                       Add forward routing rule

Flags:
  -a, --keep-alive duration   timeout for rule expiration (default 24h0m0s)

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli route add a

```

    Add app/consume routing rule

Usage:
  skywire cli route add a



Flags:
  -i, --rid string   route id
  -l, --lpk string   local public key
  -m, --lpt string   local port
  -p, --rpk string   remote pk
  -q, --rpt string   remote port

Global Flags:
  -a, --keep-alive duration   timeout for rule expiration (default 24h0m0s)
      --rpc string            RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli route add b

```

    Add intermediary forward routing rule

Usage:
  skywire cli route add b



Flags:
  -i, --rid string    route id
  -j, --nrid string   next route id
  -k, --tpid string   next transport id

Global Flags:
  -a, --keep-alive duration   timeout for rule expiration (default 24h0m0s)
      --rpc string            RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli route add c

```

    Add forward routing rule

Usage:
  skywire cli route add c



Flags:
  -i, --rid string    route id
  -j, --nrid string   next route id
  -k, --tpid string   next transport id
  -l, --lpk string    local public key
  -m, --lpt string    local port
  -p, --rpk string    remote pk
  -q, --rpt string    remote port

Global Flags:
  -a, --keep-alive duration   timeout for rule expiration (default 24h0m0s)
      --rpc string            RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli route calc

```
Calculate routes locally using transport discovery data

	skywire cli route calc <dst-pk>           - calculate route to destination
	skywire cli route calc <src-pk> <dst-pk>  - calculate route between two visors
	skywire cli route calc --enable           - enable local route calculation in visor
	skywire cli route calc --disable          - disable local route calculation in visor

Usage:
  skywire cli route calc [<src-pk>] <dst-pk>



Flags:
      --disable            disable local route calculation in visor
      --enable             enable local route calculation in visor
  -x, --max uint16         maximum hops (default 5)
  -n, --min uint16         minimum hops (default 1)
      --no-dmsg            skip direct DMSG HTTP step
      --no-http            skip direct HTTP fallback step
      --no-rpc             skip visor RPC (DmsgHTTP) step
  -t, --timeout duration   request timeout (default 30s)
  -a, --tpd string         transport discovery URL (default "http://tpd.skywire.skycoin.com")

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli route find

```
Query the Route Finder
Assumes the local visor public key as an argument if only one argument is given

Usage:
  skywire cli route find <public-key> | <public-key-visor-1> <public-key-visor-2>



Flags:
  -n, --min uint16         minimum hops (default 1)
  -x, --max uint16         maximum hops (default 1000)
  -t, --timeout duration   request timeout (default 10s)
  -a, --addr string        route finder service address (default "http://rf.skywire.skycoin.com")

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli route groups

```

    List active route groups with their consume and forward rules

Usage:
  skywire cli route groups



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli route rm

```

    Remove routing rule
    Use --all to remove all routing rules

Usage:
  skywire cli route rm [route-id]



Flags:
  -a, --all   remove all routing rules

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli route rsn-stats

```
Query the visor's embedded Route Setup Node for per-request statistics.

Shows aggregate counters (total / successful / failed / concurrency drops),
a breakdown of failures by reason, latency percentiles for successful
setups, the distribution of route lengths, the most-requested and most-
failed destination PKs, and a ring buffer of the most recent failures
with error detail.

Requires the visor to have an embedded route setup-node configured
(route_setup_sk in the visor config).

Usage:
  skywire cli route rsn-stats



Flags:
      --reset   reset all counters before reading (captures a fresh window)

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli sd

```
Display combined service discovery and transport statistics

Combines data from:
- Service Discovery: http://sd.skycoin.com/api/services
- Transport Discovery: http://tpd.skywire.skycoin.com/all-transports

Shows public keys with their services and transport counts by type.

Use --testenv or SKYWIRETEST=1 to use test deployment services.

Usage:
  skywire cli sd



Flags:
      --cds string       SD cache dir ("" to disable) (default "/tmp/sd.skycoin.com")
      --cdt string       TPD cache dir ("" to disable) (default "/tmp/tpd.skywire.skycoin.com")
      --cdu string       UT cache dir ("" to disable) (default "/tmp/ut.skywire.skycoin.com")
  -m, --cfa int          update cache files if older than n minutes (default 5)
  -c, --country string   filter by country code
      --json             print output in json
  -n, --min int          filter by minimum transport count
      --no-dmsg          skip direct DMSG HTTP step
      --no-http          skip direct HTTP fallback step
      --no-rpc           skip visor RPC (DmsgHTTP) step
  -o, --noton            do not filter by online status in UT
      --rpc string       RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
  -a, --sdurl string     service discovery url (default "http://sd.skycoin.com")
      --testenv          use test deployment
  -b, --tpdurl string    transport discovery url (default "http://tpd.skywire.skycoin.com")
  -w, --uturl string     uptime tracker url (default "http://ut.skywire.skycoin.com")
  -e, --version string   filter by version
```

### skywire cli skychat

```
Send and receive messages via skychat.

Usage:
  skywire cli skychat

Available Commands:
  listen                  Listen for incoming messages
  send                    Send a message

Flags:
      --addr string   skychat HTTP address (default "127.0.0.1:8001")
```

#### skywire cli skychat listen

```
Connect to skychat SSE endpoint and display incoming messages.

Usage:
  skywire cli skychat listen



Flags:
  -n, --net string   filter by network type (optional)

Global Flags:
      --addr string   skychat HTTP address (default "127.0.0.1:8001")
```

#### skywire cli skychat send

```
Send a message to a remote public key via skychat.

Usage:
  skywire cli skychat send



Flags:
  -m, --msg string   message to send (required)
  -n, --net string   network type: skynet or dmsg (default "skynet")
  -t, --to string    recipient public key (required)

Global Flags:
      --addr string   skychat HTTP address (default "127.0.0.1:8001")
```

### skywire cli skynet

```
Skynet provides port forwarding over the Skywire network.

Client commands connect to remote skynet servers and forward traffic to localhost.
Multiple client instances can run simultaneously with different configurations.
Each instance gets a unique name (e.g., skynet-client-8080, skynet-client-3435).

Server commands (srv) expose local ports over the network.

Usage:
  skywire cli skynet

Available Commands:
  curl                    HTTP request over skynet
  port                    Manage forwarded ports
  srv                     Skynet port forwarding server
  start                   Start skynet client to connect to a remote server
  status                  Show skynet client status
  stop                    Stop a skynet client instance

Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli skynet curl

```
Make HTTP requests over skynet routes.

The visor establishes a route to the remote visor and sends the HTTP
request through the skynet forwarding server.

URL format:
  skynet://<public-key>:<port>/path
  skynet://<public-key>/path  (port defaults to 80)

Examples:
  skywire cli skynet curl skynet://02abc.../health
  skywire cli skynet curl skynet://02abc...:8000/api/ping
  skywire cli skynet curl -d '{"key":"val"}' skynet://02abc.../endpoint

Usage:
  skywire cli skynet curl <skynet-url>



Flags:
  -d, --data string   HTTP POST data
  -o, --out string    output file path

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli skynet port

```
List, add, and remove ports forwarded over skynet and/or DMSG.

Usage:
  skywire cli skynet port

Available Commands:
  add                     Forward a local port over skynet/DMSG
  ls                      List forwarded ports
  rm                      Remove a forwarded port

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli skynet port add

```
Forward a local TCP port over skynet and/or DMSG.

Examples:
  skywire cli skynet port add 8080
  skywire cli skynet port add 8080 --label "My App" --desc "Web dashboard"
  skywire cli skynet port add 3000 --skynet --dmsg=false --landing=false

Usage:
  skywire cli skynet port add <port>



Flags:
  -d, --desc string         description shown on landing page
      --dmsg                forward over DMSG (default true)
  -l, --label string        label shown on landing page
      --landing             show link on visor landing page (default true)
      --proxy-addr string   reverse proxy to local address (e.g. 127.0.0.1:3000); for port 80 this replaces the landing page
      --skynet              forward over skynet (default true)

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli skynet port ls

```
List forwarded ports

Usage:
  skywire cli skynet port ls



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli skynet port rm

```
Remove a forwarded port

Usage:
  skywire cli skynet port rm <port>



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli skynet srv

```
Control the skynet server application.

The skynet server exposes local TCP ports over the Skywire network.
Other visors can connect to these ports using the skynet client.

Multiple server instances can run simultaneously with different ports.
Each instance gets a unique name (e.g., skynet-3435, skynet-8080).

With whitelist support, you can restrict access to specific public keys.

Usage:
  skywire cli skynet srv

Available Commands:
  start                   Start a skynet server instance
  status                  Show skynet server status
  stop                    Stop a skynet server instance

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli skynet srv start

```
Start a skynet server instance

Usage:
  skywire cli skynet srv start



Flags:
      --external           force external launcher
      --internal           force internal launcher
  -n, --name string        custom name for this server instance (default: skynet-<first-port>)
      --port uint16        routing port for communication between app and visor
  -p, --ports string       comma-separated list of local ports to expose (e.g., '8080,9000')
  -w, --whitelist string   comma-separated list of public keys allowed to connect (empty = allow all)

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli skynet srv status

```
Show status of skynet server instances.

If no name is provided, shows all skynet server instances.

Examples:
  skywire cli skynet srv status
  skywire cli skynet srv status skynet-3435

Usage:
  skywire cli skynet srv status [name]



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli skynet srv stop

```
Stop a skynet server instance by name.

If no name is provided, stops all running skynet server instances.

Examples:
  skywire cli skynet srv stop skynet-3435
  skywire cli skynet srv stop --name skynet-8080
  skywire cli skynet srv stop  # stops all skynet-* servers

Usage:
  skywire cli skynet srv stop [name]



Flags:
  -n, --name string   name of the server instance to stop

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli skynet start

```
Connect to a remote skynet server and forward traffic to a local port.

Usage:
  skywire cli skynet start



Flags:
      --external      force external launcher
      --internal      force internal launcher
  -l, --local int     local port to listen on
  -n, --name string   custom name for this client instance (default: skynet-client-<local-port>)
  -k, --pk string     remote server public key
      --port uint16   routing port for communication between app and visor
      --raw-tcp       use raw TCP forwarding instead of HTTP
  -r, --remote int    remote port to forward

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli skynet status

```
Show status of skynet client instances.

If no name is provided, shows all skynet client instances.

Examples:
  skywire cli skynet status
  skywire cli skynet status skynet-client-8080

Usage:
  skywire cli skynet status [name]



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli skynet stop

```
Stop a skynet client instance by name.

If no name is provided, stops all running skynet client instances.

Examples:
  skywire cli skynet stop skynet-client-8080
  skywire cli skynet stop --name skynet-client-3435
  skywire cli skynet stop  # stops all skynet-client-* clients

Usage:
  skywire cli skynet stop [name]



Flags:
  -n, --name string   name of the client instance to stop

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli survey

```
print the system survey

Usage:
  skywire cli survey



Flags:
  -c, --config string      optionl config file to use (i.e.: skywire-config.json)
  -D, --dmsg-disc string   value of dmsg discovery
  -p, --pkg                use package config /opt/skywire/skywire.json
  -u, --user               use config at: /home/d0mo/skywire-config.json
```

### skywire cli svc

```

    Query skywire deployment services (health, stats)

Usage:
  skywire cli svc

Available Commands:
  ar                      Address Resolver endpoints
  dmsgd                   DMSG Discovery endpoints
  health                  Check health of all deployment services
  nm                      Network Monitor status
  tpd                     Transport Discovery endpoints
```

#### skywire cli svc ar

```

    Query Address Resolver service endpoints

Usage:
  skywire cli svc ar



Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli svc dmsgd

```

    Query DMSG Discovery service endpoints

Usage:
  skywire cli svc dmsgd

Available Commands:
  all-servers                List all DMSG servers
  clients                    List clients for a specific DMSG server
  server-clients             List all clients grouped by server

Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli svc dmsgd all-servers

```
List all DMSG servers

Usage:
  skywire cli svc dmsgd all-servers



Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli svc dmsgd clients

```
List clients for a specific DMSG server

Usage:
  skywire cli svc dmsgd clients



Flags:
  -p, --pk string   server public key (required)

Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli svc dmsgd server-clients

```
List all clients grouped by server

Usage:
  skywire cli svc dmsgd server-clients



Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli svc health

```
    Check the /health endpoint of all skywire deployment services.

    By default queries via the local visor RPC (uses visor's configured URLs).
    Use --direct to query services directly from the CLI.

Usage:
  skywire cli svc health



Flags:
      --direct       query services directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli svc nm

```

    Query Network Monitor service status

Usage:
  skywire cli svc nm



Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli svc tpd

```

    Query Transport Discovery service endpoints

Usage:
  skywire cli svc tpd

Available Commands:
  bandwidth                 Bandwidth data (network-wide or per-visor)
  bandwidth-tp              Bandwidth history for a specific transport
  metrics-tp                Metrics for specific transport(s)
  metrics-visor             Metrics for specific visor(s)
  per-key-stats             Per-visor transport statistics
  stats                     Network-wide transport statistics
  versions                  Version statistics from transport discovery
  versions-pk               Version info for specific public keys
  visor-stats               Transport count statistics for a specific visor

Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli svc tpd bandwidth

```
Bandwidth data (network-wide or per-visor)

Usage:
  skywire cli svc tpd bandwidth



Flags:
  -p, --pk string   visor public key (omit for network-wide)

Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli svc tpd bandwidth-tp

```
Bandwidth history for a specific transport

Usage:
  skywire cli svc tpd bandwidth-tp



Flags:
  -i, --id string   transport ID (required)

Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli svc tpd metrics-tp

```
Metrics for specific transport(s)

Usage:
  skywire cli svc tpd metrics-tp



Flags:
  -i, --ids string   comma-separated transport IDs (required)

Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli svc tpd metrics-visor

```
Metrics for specific visor(s)

Usage:
  skywire cli svc tpd metrics-visor



Flags:
  -p, --pks string   comma-separated public keys (required)

Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli svc tpd per-key-stats

```
Per-visor transport statistics

Usage:
  skywire cli svc tpd per-key-stats



Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli svc tpd stats

```
Network-wide transport statistics

Usage:
  skywire cli svc tpd stats



Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli svc tpd versions

```
Version statistics from transport discovery

Usage:
  skywire cli svc tpd versions



Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli svc tpd versions-pk

```
Version info for specific public keys

Usage:
  skywire cli svc tpd versions-pk



Flags:
  -p, --pks string   comma-separated public keys (required)

Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli svc tpd visor-stats

```
Transport count statistics for a specific visor

Usage:
  skywire cli svc tpd visor-stats



Flags:
  -p, --pk string   visor public key (required)

Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli tp

```
Display and manage transports of the local visor

	Transports are bidirectional communication protocols
	used between two Skywire Visors (or Transport Edges)

	Each Transport is represented as a unique 16 byte (128 bit)
	UUID value called the Transport ID
	and has a Transport Type that identifies
	a specific implementation of the Transport.

	Types: stcp stcpr sudph dmsg

Usage:
  skywire cli tp


Local transports:
  add                     Add transport(s) to one or more remote public keys
  disc                    Discover remote transport(s)
  rm                      Remove transport(s) by id

Network queries:
  metrics                 Transport discovery bandwidth metrics
  net-stats               Network-wide transport statistics
  tpd-health              Transport discovery health and version info
  tpd-stats               List visors by transport count from transport discovery
  tree                    tree map of transports on the skywire network
  uptime                  query TPD integrated transport-level uptime
  v                       List public visors

Configuration:
  auto                    Control public autoconnect
  id                      Compute the deterministic transport ID for a given PK pair and type
  sync                    Control transport discovery data sync


Flags:
  -t, --types strings     show transport(s) type(s) comma-separated
  -p, --pks strings       show transport(s) for public key(s) comma-separated
  -l, --logs              show transport logs (default true)
  -m, --more              show more info
  -b, --bw int            show bandwidth usage for last N days (0 = disabled)
      --inactive          show bandwidth for inactive transports (requires --bw)
      --cfu string        UT cache file location. (default "/tmp/ut.json")
      --cfsp string       SD cache file location (default "/tmp/proxysd.json")
      --cfsv string       SD cache file location (default "/tmp/vpnsd.json")
      --cfsvisor string   SD cache file location (default "/tmp/visorsd.json")
  -a, --sdurl string      service discovery url (default "http://sd.skycoin.com")
  -w, --uturl string      uptime tracker url (TPD integrated) (default "http://tpd.skywire.skycoin.com")
  -i, --id string         display transport matching ID
  -u, --tptypes           display transport types used by the local visor
  -s, --stats             show transport statistics (count by type, unique visors)
      --rpc string        RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
      --remote strings    list transports on remote visor(s) via TPS (comma-separated PKs)
```

#### skywire cli tp add

```

    Add transport(s)
		Accepts one or more remote public keys as arguments.
		If the transport type is unspecified,
		the visor will attempt to establish a transport
		in the following order: stcpr, sudph, dmsg

Usage:
  skywire cli tp add <public-key> [public-key]...

Available Commands:
  edge                    Add transports to all edges of a public key
  pv                      Add transports to public visors

Flags:
  -r, --rpk string         remote public key.
  -t, --type string        type of transport to add.
  -o, --timeout duration   if specified, sets an operation timeout
  -n, --retries int        number of times to retry per transport type (default 1)
      --rpc string         RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
  -u, --user               set transport label to 'user' (default is 'skycoin')
      --no-register        skip transport discovery registration (implies --user)
      --no-probe           skip dmsg port 136 reachability probe before adding transport
      --remote strings     request transport via TPS on remote visor(s) (comma-separated PKs)
      --addr string        remote address (ip:port) for stcp transport
      --no-rpc             skip visor RPC (DmsgHTTP) step
      --no-dmsg            skip direct DMSG HTTP step
      --no-http            skip direct HTTP fallback step
```

##### skywire cli tp add edge

```
Query transport discovery for all transports connected to the specified
public key, then attempt to add transports to each of those edge keys.

This is useful for connecting to all visors that a specific visor is
connected to (e.g., connecting to all edges of a well-connected hub).

Usage:
  skywire cli tp add edge <public-key>



Flags:
  -b, --batch int          number of transports to add in parallel (default 5)
      --rpc string         RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
  -o, --timeout duration   timeout for each transport addition (default 30s)
  -d, --tpdurl string      transport discovery url
  -v, --verbose            verbose output
```

##### skywire cli tp add pv

```
Add transports to public visors, starting with those that have the most transports.

  Fetches public visors from service discovery and attempts to establish
  transports to the top N visors (by transport count). This is useful for
  improving network connectivity and reachability.

Usage:
  skywire cli tp add pv



Flags:
  -m, --cfa int            update cache files if older than n minutes (default 5)
      --cfdd string        Dmsg Discovery cache file location (default "/tmp/dmsgd.json")
      --cfs string         SD cache file location (default "/tmp/visorsd.json")
      --cft string         TPD cache file location (default "/tmp/tpd.json")
      --cfu string         UT cache file location (default "/tmp/ut.json")
  -n, --count int          number of public visors to add transports to (default 5)
      --dmsg string        dmsg discovery url (default "http://dmsgd.skywire.skycoin.com")
      --force              attempt dmsg transport without checking dmsg discovery
      --min int            minimum transport count for target visors
      --no-dmsg            skip direct DMSG HTTP step
      --no-http            skip direct HTTP fallback step
      --no-rpc             skip visor RPC (DmsgHTTP) step
  -f, --noton              do not filter by online status
      --remote strings     request public visor transports on remote visor(s) via TPS (comma-separated PKs)
      --retries int        number of times to retry per transport type (default 1)
      --rpc string         RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
  -a, --sdurl string       service discovery url (default "http://sd.skycoin.com")
  -o, --timeout duration   operation timeout
  -d, --tpdurl string      transport discovery url (default "http://tpd.skywire.skycoin.com")
  -t, --type string        transport type (stcpr, sudph, dmsg)
  -w, --uturl string       uptime tracker url (TPD integrated) (default "http://tpd.skywire.skycoin.com")
```

#### skywire cli tp auto

```
Start or stop public autoconnect

	skywire cli tp auto      - show status
	skywire cli tp auto on   - start autoconnect
	skywire cli tp auto off  - stop autoconnect

Usage:
  skywire cli tp auto [on|off]



Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli tp disc

```

    Discover remote transport(s) by ID or public key

Usage:
  skywire cli tp disc



Flags:
  -i, --id string       obtain transport of given ID
  -p, --pk string       obtain transports by public key
      --tpdurl string   transport discovery url (default "http://tpd.skywire.skycoin.com")
      --http            query transport discovery via HTTP, bypass RPC
```

#### skywire cli tp id

```
Compute the deterministic transport ID (UUID) for a given transport type
and pair of public keys. This is purely a local computation — no RPC, no
discovery query — and mirrors transport.MakeTransportID().

The returned ID is independent of PK order: id(T, A, B) == id(T, B, A).

Valid transport types: dmsg, stcp, stcpr, sudph (default: dmsg)

Usage:
  skywire cli tp id <pk1> <pk2>



Flags:
  -t, --type string   transport type (dmsg, stcp, stcpr, sudph) (default "dmsg")
```

#### skywire cli tp metrics

```
	Query transport discovery for bandwidth metrics.

	Shows verified bandwidth — the amount both transport edges agree on.
	Default: aggregate bandwidth per visor (public key).
	With --by-transport: show bandwidth per transport ID.
	With --tree: tree view with visors and their transports.

Usage:
  skywire cli tp metrics



Flags:
  -d, --days int        number of days of metrics (0 = all, max 35) (default 1)
  -p, --pk string       filter by public key
  -n, --top int         show only top N results by bandwidth (0 = all)
  -t, --by-transport    show bandwidth per transport ID instead of per visor
      --tree            tree view: visors with their transports as children
  -v, --verbose         show full public keys (with --by-transport)
      --tpdurl string   transport discovery url (default "http://tpd.skywire.skycoin.com")
      --rpc string      RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
      --no-rpc          skip visor RPC (DmsgHTTP) step
      --no-dmsg         skip direct DMSG HTTP step
      --no-http         skip direct HTTP fallback step
```

#### skywire cli tp net-stats

```
Query the transport discovery for aggregate network statistics.
Shows total transport count by type and unique visor count.

Usage:
  skywire cli tp net-stats



Flags:
      --no-dmsg      skip direct DMSG HTTP step
      --no-http      skip direct HTTP fallback step
      --no-rpc       skip visor RPC (DmsgHTTP) step
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli tp rm

```

    Remove transport(s) by id

    Use --remote with --tp to remove transports on a remote visor via the embedded TPS

Usage:
  skywire cli tp rm



Flags:
  -a, --all              remove all transports
  -i, --id string        remove transport of given ID
      --remote strings   remove transport on remote visor(s) via embedded TPS (comma-separated PKs)
      --tp strings       transport ID(s) to remove on remote visor (comma-separated, use with --remote)
      --rpc string       RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli tp sync

```
Control transport discovery data sync (bandwidth/latency)

	skywire cli tp sync           - show status
	skywire cli tp sync --enable  - enable sync
	skywire cli tp sync --disable - disable sync

Usage:
  skywire cli tp sync



Flags:
      --disable   disable transport discovery data sync
      --enable    enable transport discovery data sync
```

#### skywire cli tp tpd-health

```
Transport discovery health and version info

Usage:
  skywire cli tp tpd-health



Flags:
      --no-dmsg      skip direct DMSG HTTP step
      --no-http      skip direct HTTP fallback step
      --no-rpc       skip visor RPC (DmsgHTTP) step
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli tp tpd-stats

```
Query the transport discovery for per-visor transport statistics.
Shows transport counts broken down by type, sorted by total.

Examples:
  skywire cli tp tpd-stats --top 20
  skywire cli tp tpd-stats --type stcpr --min 5
  skywire cli tp tpd-stats --json

Usage:
  skywire cli tp tpd-stats



Flags:
      --min int       minimum transport count to display
      --no-dmsg       skip direct DMSG HTTP step
      --no-http       skip direct HTTP fallback step
      --no-rpc        skip visor RPC (DmsgHTTP) step
      --rpc string    RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
  -n, --top int       show top N visors by transport count (0 = all)
  -t, --type string   filter by transport type (e.g. stcpr, sudph)
```

#### skywire cli tp tree

```
display a tree representation of transports from TPD

http://tpd.skywire.skycoin.com/all-transports

Set cache file location to "" to avoid using cache files

Usage:
  skywire cli tp tree



Flags:
  -k, --source string    root node ; defaults to visor with most transports
  -d, --dest string      map route between source and dest
  -a, --tpdurl string    transport discovery url (default "http://tpd.skywire.skycoin.com")
  -w, --uturl string     uptime tracker url (TPD integrated) (default "http://tpd.skywire.skycoin.com")
  -r, --raw              print raw json data
  -p, --pretty           print pretty json data
  -o, --noton            do not filter by online status in UT
  -g, --good             do not display transports for offline visors
      --cft string       TPD cache file location (default "/tmp/tpd.json")
      --cfu string       UT cache file location. (default "/tmp/ut.json")
  -m, --cfa int          update cache files if older than n minutes (default 5)
  -P, --pad int          padding between tree and tpid (default 15)
  -s, --stats            return only statistics
  -v, --version string   filter by minimum version (e.g., 1.3.34)
  -K, --keys             output only reachable public keys (requires -k source)
  -H, --hops int         max hops from source for --keys mode (1 or 2) (default 2)
  -x, --no-self          exclude source key from --keys output
      --no-rpc           skip visor RPC (DmsgHTTP) step
      --no-dmsg          skip direct DMSG HTTP step
      --no-http          skip direct HTTP fallback step
```

#### skywire cli tp uptime

```
Transport-level uptime from transport-discovery.

Default (no flags): GET /uptimes/transports — every known transport, with
v1 (id+on), v2 (+ daily %), or v3 (+ timeline bitmap) fields.

Filtered modes (mutually exclusive):
  --ids a,b,c       /metrics/uptime/{ids}       — specific transports
  --visors pk1,pk2  /metrics/uptime/visor/{pks} — transports touching these visors
  --metrics         /metrics/uptime             — network-wide aggregate only

Combine with --on to show only currently-online transports, --type to filter by
transport type, or --json to dump raw JSON for piping.

Usage:
  skywire cli tp uptime



Flags:
      --cache-age int      re-fetch if cache is older than N minutes (0 disables) (default 5)
      --cache-dir string   cache directory ("" disables) (default "/tmp/tpd.skywire.skycoin.com")
      --ids strings        filter to these transport IDs (comma-separated UUIDs) — uses /metrics/uptime/{ids}
      --json               emit raw JSON
  -m, --metrics            fetch network-wide /metrics/uptime aggregate instead of per-transport rows
      --no-dmsg            skip direct DMSG HTTP step
      --no-http            skip direct HTTP fallback step
      --no-rpc             skip visor RPC (DmsgHTTP) step
  -o, --on                 only include currently online transports
      --timeout duration   HTTP timeout (default 30s)
  -t, --type string        filter by transport type (stcpr / sudph / dmsg / stcp)
      --url string         transport-discovery base URL (default "http://tpd.skywire.skycoin.com")
  -v, --v string           response version (v1|v2|v3) (default "v2")
      --visors strings     filter to transports touching these visor PKs (comma-separated) — uses /metrics/uptime/visor/{pks}
```

#### skywire cli tp v

```
List public visors from service discovery
http://sd.skycoin.com/api/services?type=visor
http://sd.skycoin.com/api/services?type=visor&country=US

Set cache file location to "" to avoid using cache files

Usage:
  skywire cli tp v



Flags:
  -m, --cfa int          update cache files if older than n minutes (default 5)
      --cfs string       SD cache file location (default "/tmp/visorsd.json")
      --cfu string       UT cache file location. (default "/tmp/ut.json")
  -c, --country string   filter by country code
      --json             print output in json
      --no-dmsg          skip direct DMSG HTTP step
      --no-http          skip direct HTTP fallback step
      --no-rpc           skip visor RPC (DmsgHTTP) step
  -o, --noton            do not filter by online status in UT
  -k, --pk string        check visor service discovery for public key
  -r, --raw              print raw json data
  -a, --sdurl string     service discovery url (default "http://sd.skycoin.com")
  -s, --stats            return only a count of the results
  -w, --uturl string     uptime tracker url (TPD integrated) (default "http://tpd.skywire.skycoin.com")
  -e, --version string   filter by version
```

#### skywire cli tp viz

```
Start a web-based network graph visualizer for Skywire transport discovery data.

Modes:
  Standalone (default): Runs a local HTTP server showing network-wide transport data.
  Visor-embedded (--visor): Requests the running visor to start its embedded UI server,
    which has direct access to local transport/route data without RPC overhead.

Displays an interactive force-directed graph showing:
- Visors as nodes (sized by connection count)
- Transports as edges (colored by type: STCPR=green, SUDPH=blue, DMSG=yellow)
- Uptime status (green=online, red=offline, yellow=not in UT)
- Service discovery overlay (proxy, VPN, visor services)

Data is cached locally to reduce load on the discovery services.
Auto-refresh keeps the cache updated at the specified interval.

Usage:
  skywire cli tp viz



Flags:
  -a, --addr string         address to bind to (standalone mode) (default "127.0.0.1")
  -p, --port int            port to listen on (standalone mode) (default 8080)
      --cdt string          TPD cache dir ("" to disable) (default "/tmp/tpd.skywire.skycoin.com")
      --cdu string          UT cache dir ("" to disable) (default "/tmp/tpd.skywire.skycoin.com")
      --cds string          SD cache dir ("" to disable) (default "/tmp/sd.skycoin.com")
      --cdd string          DMSG cache dir ("" to disable) (default "/tmp/dmsgd.skywire.skycoin.com")
  -m, --cfa int             update cache files if older than n minutes (default 5)
      --tpd-url string      transport discovery URL (default "http://tpd.skywire.skycoin.com")
  -w, --ut-url string       uptime tracker URL (default "http://tpd.skywire.skycoin.com")
      --sd-url string       service discovery URL (default "http://sd.skycoin.com")
      --dmsg-url string     DMSG discovery URL (default "http://dmsgd.skywire.skycoin.com")
      --no-cache            disable caching, always fetch fresh data
      --no-auto-refresh     disable auto-refresh of cache
      --survey-dir string   directory containing visor surveys for IP-based grouping (node-info.json files)
  -v, --visor               request the running visor to start its embedded UI server
      --stop                request the running visor to stop its embedded UI server
      --status              check the status of the visor's embedded UI server
      --rpc string          visor RPC address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli tps

```
Commands to control the embedded Transport Setup Node (TPS).

The embedded TPS allows this visor to manage transports on other visors
that trust this visor's TPS public key.

To enable embedded TPS, set 'tps_sk' in the visor config.

Usage:
  skywire cli tps

Available Commands:
  add                     Add transport on target visor
  list                    List transports on target visor
  rm                      Remove transport on target visor

Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli tps add

```
Request the target visor to create a transport to the remote visor.

The target visor must trust this visor's TPS public key.

Usage:
  skywire cli tps add



Flags:
  -t, --target string   target visor public key (visor to add transport on)
  -r, --remote string   remote visor public key (other transport edge)
  -T, --type string     transport type (stcpr, sudph, dmsg) (default "stcpr")

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli tps list

```
Get the list of transports from a target visor.

Usage:
  skywire cli tps list



Flags:
  -t, --target string   target visor public key

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli tps rm

```
Request the target visor to remove a transport by ID.

Usage:
  skywire cli tps rm



Flags:
  -t, --target string   target visor public key
  -i, --id string       transport ID to remove

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli ut

```
query uptime tracker

http://ut.skywire.skycoin.com/uptimes?v=v2

Check local visor daily uptime percent with:

$ skywire-cli ut -n0 -k $(skywire-cli visor pk)

Set cache dir to "" to avoid using cache files

Use --testenv or SKYWIRETEST=1 to use test deployment services.

Usage:
  skywire cli ut

Available Commands:
  mdisc                   query DMSG-discovery integrated uptime tracker
  sd                      query service-discovery integrated uptime tracker
  tpd                     query TPD integrated uptime tracker

Flags:
      --cdt string           TPD cache dir ("" to disable)
                              (default "/tmp/tpd.skywire.skycoin.com")
      --cdu string           UT cache dir ("" to disable)
                              (default "/tmp/ut.skywire.skycoin.com")
  -m, --cfa int              update cache files if older than n minutes
                              (default 5)
  -l, --list-versions        list PKs with their versions
      --max-tp int           filter visors with at most N transports (fetches TPD data) (default -1)
  -n, --min int              list visors meeting minimum uptime percentage
                              (default 75)
      --min-version string   filter visors with version >= specified (e.g. v1.3.34)
      --no-dmsg              skip direct DMSG HTTP step
      --no-http              skip direct HTTP fallback step
      --no-rpc               skip visor RPC (DmsgHTTP) step
  -o, --on                   list currently online visors
  -k, --pk string            check uptime for the specified key
      --rpc string           RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
  -s, --stats                count the number of results
  -t, --stats2               count of versions
      --testenv              use test deployment
      --tpdurl string        transport discovery url (default "http://tpd.skywire.skycoin.com")
  -u, --url string           specify alternative uptime tracker url
                              (default "http://ut.skywire.skycoin.com")
  -v, --version string       filter visors by exact version
```

#### skywire cli ut mdisc

```
Query the DMSG-Discovery integrated uptime endpoint.

http://dmsgd.skywire.skycoin.com/uptimes

Default is v2 (includes daily percentages). Pass -T / --timeline to
request v3 and render the per-5-minute bitmap as 24 hourly blocks.

Usage:
  skywire cli ut mdisc

Available Commands:
  graph                   render an uptime timeline graph (compact shaded-block bars)

Flags:
  -a, --all                  include every day the server returned
  -m, --cache-age int        re-fetch if cache is older than N minutes (0 disables) (default 5)
      --cache-dir string     cache directory ("" disables cache) (default "/tmp/dmsgd.skywire.skycoin.com")
  -d, --days int             number of most-recent days to include (0 = latest day only)
      --json                 emit raw JSON
  -l, --list-versions        list version distribution (with --stats) or pk+version pairs
  -n, --min-daily int        only visors whose worst daily-uptime is >= this percent
      --min-version string   filter visors with version >= this (e.g. v1.3.40)
      --no-dmsg              skip direct DMSG HTTP step
      --no-http              skip direct HTTP fallback step
      --no-rpc               skip visor RPC (DmsgHTTP) step
  -o, --on                   only include online visors
  -k, --pk string            only show PKs matching this substring
      --since string         include days on or after this date (YYYY-MM-DD)
  -s, --stats                print count of matching visors only
      --timeout duration     HTTP timeout (default 30s)
      --until string         include days on or before this date (YYYY-MM-DD)
      --url string           discovery base URL (default "http://dmsgd.skywire.skycoin.com")
  -v, --v string             response version (v1|v2) (default "v2")
      --version string       filter visors by exact version
      --visors strings       server-side filter: only return these PKs (comma-separated)
```

##### skywire cli ut mdisc graph

```
Render the v3 per-5-minute timeline as shaded 1-hour blocks.

Default: one line per visor, all days available server-side, compact "<pk> <bar>" format.

Modes (mutually exclusive after flags):
  (default)   single-line bar per visor, spans the selected day range
  --per-day   break each visor's bar onto one row per day
  --hours N   rolling window of the last N hours ending at now

Date range flags (--days/--since/--until/--all) apply to the default
and --per-day modes; --hours ignores them.

Usage:
  skywire cli ut mdisc graph



Flags:
  -a, --all                  include every day the server returned (default when --days/--since/--until not set)
  -m, --cache-age int        re-fetch if cache is older than N minutes (0 disables) (default 5)
      --cache-dir string     cache directory ("" disables cache) (default "/tmp/dmsgd.skywire.skycoin.com")
  -d, --days int             number of most-recent days to include (0 = all available; ignored with --hours)
      --hours int            rolling-window mode: show last N hours ending at now
      --json                 emit raw JSON instead of rendering
      --min-version string   filter visors with version >= this
      --no-dmsg              skip direct DMSG HTTP step
      --no-http              skip direct HTTP fallback step
      --no-rpc               skip visor RPC (DmsgHTTP) step
  -o, --on                   only include online visors
      --per-day              one row per day instead of a single concatenated bar
  -k, --pk string            only show PKs matching this substring
      --since string         include days on or after this date (YYYY-MM-DD)
      --timeout duration     HTTP timeout (default 30s)
      --until string         include days on or before this date (YYYY-MM-DD)
      --url string           discovery base URL (default "http://dmsgd.skywire.skycoin.com")
  -v, --verbose              include version / online state / range header per visor
      --version string       filter visors by exact version
      --visors strings       server-side filter: only return these PKs (comma-separated)
```

#### skywire cli ut sd

```
Query the Service-Discovery integrated uptime endpoint.

http://sd.skycoin.com/uptimes

Default is v2 (includes daily percentages). Pass -T / --timeline to
request v3 and render the per-5-minute bitmap as 24 hourly blocks.

Usage:
  skywire cli ut sd

Available Commands:
  graph                   render an uptime timeline graph (compact shaded-block bars)

Flags:
  -a, --all                  include every day the server returned
  -m, --cache-age int        re-fetch if cache is older than N minutes (0 disables) (default 5)
      --cache-dir string     cache directory ("" disables cache) (default "/tmp/sd.skycoin.com")
  -d, --days int             number of most-recent days to include (0 = latest day only)
      --json                 emit raw JSON
  -l, --list-versions        list version distribution (with --stats) or pk+version pairs
  -n, --min-daily int        only visors whose worst daily-uptime is >= this percent
      --min-version string   filter visors with version >= this (e.g. v1.3.40)
      --no-dmsg              skip direct DMSG HTTP step
      --no-http              skip direct HTTP fallback step
      --no-rpc               skip visor RPC (DmsgHTTP) step
  -o, --on                   only include online visors
  -k, --pk string            only show PKs matching this substring
      --since string         include days on or after this date (YYYY-MM-DD)
  -s, --stats                print count of matching visors only
      --timeout duration     HTTP timeout (default 30s)
      --until string         include days on or before this date (YYYY-MM-DD)
      --url string           discovery base URL (default "http://sd.skycoin.com")
  -v, --v string             response version (v1|v2) (default "v2")
      --version string       filter visors by exact version
      --visors strings       server-side filter: only return these PKs (comma-separated)
```

##### skywire cli ut sd graph

```
Render the v3 per-5-minute timeline as shaded 1-hour blocks.

Default: one line per visor, all days available server-side, compact "<pk> <bar>" format.

Modes (mutually exclusive after flags):
  (default)   single-line bar per visor, spans the selected day range
  --per-day   break each visor's bar onto one row per day
  --hours N   rolling window of the last N hours ending at now

Date range flags (--days/--since/--until/--all) apply to the default
and --per-day modes; --hours ignores them.

Usage:
  skywire cli ut sd graph



Flags:
  -a, --all                  include every day the server returned (default when --days/--since/--until not set)
  -m, --cache-age int        re-fetch if cache is older than N minutes (0 disables) (default 5)
      --cache-dir string     cache directory ("" disables cache) (default "/tmp/sd.skycoin.com")
  -d, --days int             number of most-recent days to include (0 = all available; ignored with --hours)
      --hours int            rolling-window mode: show last N hours ending at now
      --json                 emit raw JSON instead of rendering
      --min-version string   filter visors with version >= this
      --no-dmsg              skip direct DMSG HTTP step
      --no-http              skip direct HTTP fallback step
      --no-rpc               skip visor RPC (DmsgHTTP) step
  -o, --on                   only include online visors
      --per-day              one row per day instead of a single concatenated bar
  -k, --pk string            only show PKs matching this substring
      --since string         include days on or after this date (YYYY-MM-DD)
      --timeout duration     HTTP timeout (default 30s)
      --until string         include days on or before this date (YYYY-MM-DD)
      --url string           discovery base URL (default "http://sd.skycoin.com")
  -v, --verbose              include version / online state / range header per visor
      --version string       filter visors by exact version
      --visors strings       server-side filter: only return these PKs (comma-separated)
```

#### skywire cli ut tpd

```
Query the Transport-Discovery integrated uptime endpoint.

http://tpd.skywire.skycoin.com/uptimes

Same response shape as sd / mdisc / ut — v1 (pk+on), v2 (+ daily %),
v3 (+ per-5-minute timeline bitmap). Default is v2; pass -T / --timeline
to request v3 and render the bitmap as 24 hourly blocks.

Populated by visor heartbeats + transport registrations; the same
data feeds into the rewards pipeline.

Usage:
  skywire cli ut tpd

Available Commands:
  graph                   render an uptime timeline graph (compact shaded-block bars)

Flags:
  -a, --all                  include every day the server returned
  -m, --cache-age int        re-fetch if cache is older than N minutes (0 disables) (default 5)
      --cache-dir string     cache directory ("" disables cache) (default "/tmp/tpd.skywire.skycoin.com")
  -d, --days int             number of most-recent days to include (0 = latest day only)
      --json                 emit raw JSON
  -l, --list-versions        list version distribution (with --stats) or pk+version pairs
  -n, --min-daily int        only visors whose worst daily-uptime is >= this percent
      --min-version string   filter visors with version >= this (e.g. v1.3.40)
      --no-dmsg              skip direct DMSG HTTP step
      --no-http              skip direct HTTP fallback step
      --no-rpc               skip visor RPC (DmsgHTTP) step
  -o, --on                   only include online visors
  -k, --pk string            only show PKs matching this substring
      --since string         include days on or after this date (YYYY-MM-DD)
  -s, --stats                print count of matching visors only
      --timeout duration     HTTP timeout (default 30s)
      --until string         include days on or before this date (YYYY-MM-DD)
      --url string           discovery base URL (default "http://tpd.skywire.skycoin.com")
  -v, --v string             response version (v1|v2) (default "v2")
      --version string       filter visors by exact version
      --visors strings       server-side filter: only return these PKs (comma-separated)
```

##### skywire cli ut tpd graph

```
Render the v3 per-5-minute timeline as shaded 1-hour blocks.

Default: one line per visor, all days available server-side, compact "<pk> <bar>" format.

Modes (mutually exclusive after flags):
  (default)   single-line bar per visor, spans the selected day range
  --per-day   break each visor's bar onto one row per day
  --hours N   rolling window of the last N hours ending at now

Date range flags (--days/--since/--until/--all) apply to the default
and --per-day modes; --hours ignores them.

Usage:
  skywire cli ut tpd graph



Flags:
  -a, --all                  include every day the server returned (default when --days/--since/--until not set)
  -m, --cache-age int        re-fetch if cache is older than N minutes (0 disables) (default 5)
      --cache-dir string     cache directory ("" disables cache) (default "/tmp/tpd.skywire.skycoin.com")
  -d, --days int             number of most-recent days to include (0 = all available; ignored with --hours)
      --hours int            rolling-window mode: show last N hours ending at now
      --json                 emit raw JSON instead of rendering
      --min-version string   filter visors with version >= this
      --no-dmsg              skip direct DMSG HTTP step
      --no-http              skip direct HTTP fallback step
      --no-rpc               skip visor RPC (DmsgHTTP) step
  -o, --on                   only include online visors
      --per-day              one row per day instead of a single concatenated bar
  -k, --pk string            only show PKs matching this substring
      --since string         include days on or after this date (YYYY-MM-DD)
      --timeout duration     HTTP timeout (default 30s)
      --until string         include days on or before this date (YYYY-MM-DD)
      --url string           discovery base URL (default "http://tpd.skywire.skycoin.com")
  -v, --verbose              include version / online state / range header per visor
      --version string       filter visors by exact version
      --visors strings       server-side filter: only return these PKs (comma-separated)
```

### skywire cli util

```
Bundled utility commands

Usage:
  skywire cli util

Available Commands:
  edit                    Terminal text editor (femto)
  got                     HTTP client with concurrent downloads
  jq                      jq-like JSON processor (gojq)
  serve                   Serve static files over HTTP
```

#### skywire cli util edit

```
Embedded terminal text editor with syntax highlighting (Ctrl+S save, Ctrl+Q quit)

Usage:
  skywire cli util edit [file]


```

#### skywire cli util got

```
HTTP client utility with concurrent chunked downloads (RFC 7233),
SOCKS5 proxy support, and general-purpose HTTP requests.

  Default (no subcommand): concurrent download
  got dl <URL>             concurrent chunked download
  got req <METHOD> <URL>   general HTTP request
  got head <URL>           HEAD request (show headers)

Usage:
  skywire cli util got

Available Commands:
  dl                      Download files with concurrent chunks
  head                    Show response headers (HEAD request)
  req                     Perform an HTTP request
```

##### skywire cli util got dl

```
Download files using concurrent chunked HTTP range requests (RFC 7233).
Falls back to single-stream download when the server doesn't support ranges.

Usage:
  skywire cli util got dl <URL> [URL...]



Flags:
  -A, --agent string       user agent string
      --chunk-size uint    chunk size in bytes (0 = auto)
  -c, --concurrency uint   number of concurrent chunks (0 = auto)
  -d, --dir string         output directory
  -H, --header strings     HTTP header "Key: Value"
  -o, --output string      output file path
  -x, --proxy string       SOCKS5 proxy address (host:port)
  -r, --resume             resume interrupted download
```

##### skywire cli util got head

```
Show response headers (HEAD request)

Usage:
  skywire cli util got head <URL>



Flags:
  -A, --agent string     user agent string
  -H, --header strings   HTTP header "Key: Value"
  -x, --proxy string     SOCKS5 proxy address (host:port)
```

##### skywire cli util got req

```
Perform a general HTTP request (GET, POST, PUT, DELETE, PATCH, etc).

Examples:
  got req GET https://example.com/api/data
  got req POST https://example.com/api -D '{"key":"value"}' -H "Content-Type: application/json"
  got req PUT https://example.com/api -D @payload.json

Usage:
  skywire cli util got req <METHOD> <URL>



Flags:
  -A, --agent string     user agent string
  -D, --data string      request body (or @filename to read from file)
  -H, --header strings   HTTP header "Key: Value"
  -o, --output string    write response body to file
  -x, --proxy string     SOCKS5 proxy address (host:port)
  -v, --verbose          print response headers
```

#### skywire cli util jq

```
Process JSON using jq filter syntax (powered by gojq)

Usage:
  skywire cli util jq <filter> [file...]



Flags:
  -c, --compact-output   compact output (no pretty printing)
  -n, --null-input       use null as input instead of reading from stdin
  -r, --raw-output       output raw strings without JSON quotes
  -s, --slurp            read all inputs into an array
```

#### skywire cli util serve

```
Start a local HTTP file server for a directory.

Use with skynet port forwarding to host a website:

  skywire cli util serve /path/to/site
  # prints the listen address, e.g. 127.0.0.1:43210
  # then register it:
  skywire cli skynet port add 80 --proxy-addr 127.0.0.1:43210

Usage:
  skywire cli util serve <directory>



Flags:
  -a, --addr string   listen address (default: random port on localhost) (default "127.0.0.1:0")
```

### skywire cli visor

```
Query the Skywire Visor

Usage:
  skywire cli visor


Lifecycle:
  halt                     Stop a running visor
  ready                    Wait for visor startup to complete
  reinit                   Reinitiate modules
  start                    start visor

Identity & version:
  info                     Summary of visor info
  pk                       Public key of the visor
  user                     Show the user the visor process is running as
  ver                      Version and build info

Network state:
  dmsg-servers             List connected DMSG servers with latencies
  ip                       IP information of network
  ports                    List of Ports

Subsystems:
  app                      App settings
  hv                       Hypervisor
  proxies                  Show embedded resolving-proxy status (dmsgweb, skynetweb)
  reward                   Show reward history for a visor

Diagnostics:
  go                       Go runtime statistics
  log                      Visor runtime logs
  ping                     Ping commands for testing visor connectivity


Flags:
      --geoip string   url of geoip service (default "http://ip.skycoin.com")
      --rpc string     RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor app

```

  App settings

Usage:
  skywire cli visor app

Available Commands:
  arg                     App args
  deregister              Deregister app
  log                     Logs from app
  ls                      List apps
  register                Register app
  start                   Launch app
  stop                    Halt app

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor app arg

```
App args

Usage:
  skywire cli visor app arg

Available Commands:
  autostart               Set app autostart
  killswitch              Set app killswitch
  netifc                  Set app network interface
  passcode                Set app passcode
  secure                  Set app secure

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

###### skywire cli visor app arg autostart

```
Set app autostart

Usage:
  skywire cli visor app arg autostart <name> (true|false)



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

###### skywire cli visor app arg killswitch

```

  Set app killswitch

Usage:
  skywire cli visor app arg killswitch <name> (true|false)



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

###### skywire cli visor app arg netifc

```
Set app network interface.

  "remove" is a special arg to remove the netifc

Usage:
  skywire cli visor app arg netifc <name> <interface>



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

###### skywire cli visor app arg passcode

```

  Set app passcode.

  "remove" is a special arg to remove the passcode

Usage:
  skywire cli visor app arg passcode <name> <passcode>



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

###### skywire cli visor app arg secure

```

  Set app secure

Usage:
  skywire cli visor app arg secure <name> (true|false)



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor app deregister

```

  Deregister app

Usage:
  skywire cli visor app deregister



Flags:
  -k, --procKey string   proc key of the app to deregister

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor app log

```

  Logs from app since RFC3339Nano-formatted timestamp.

  "beginning" is a special timestamp to fetch all the logs

Usage:
  skywire cli visor app log <name> <timestamp>



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor app ls

```

  List apps

Usage:
  skywire cli visor app ls



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor app register

```

  Register app

Usage:
  skywire cli visor app register



Flags:
  -a, --appname string     name of the app
  -p, --localpath string   path of the local folder (default "./local")

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor app start

```

  Launch app

Usage:
  skywire cli visor app start <name>



Flags:
      --external   force external launcher
      --internal   force internal launcher

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor app stop

```

  Halt app

Usage:
  skywire cli visor app stop <name>



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor dmsg-servers

```

  List of connected DMSG servers sorted by latency (lowest first)

Usage:
  skywire cli visor dmsg-servers



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor go

```

  Returns Go runtime statistics including goroutine count and memory usage

Usage:
  skywire cli visor go



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor halt

```

  Stop a running visor

Usage:
  skywire cli visor halt



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor hv

```

  Hypervisor

  Access the hypervisor UI
  View remote hypervisor public key

Usage:
  skywire cli visor hv

Available Commands:
  cpk                     Public key of remote hypervisor(s) set in config
  disable                 Disable hypervisor UI at runtime
  enable                  Enable hypervisor UI at runtime
  pk                      Public key of remote hypervisor(s)
  status                  Check if hypervisor is enabled
  ui                      open Hypervisor UI in default browser

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor hv cpk

```

  Public key of remote hypervisor(s) set in config

Usage:
  skywire cli visor hv cpk



Flags:
  -i, --input string   path of input config file.
  -p, --pkg            read from /opt/skywire/skywire.json

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor hv disable

```

  Disable hypervisor UI at runtime.
  Use -w to also persist the change to the config file.

Usage:
  skywire cli visor hv disable



Flags:
  -w, --persist   write change to config file

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor hv enable

```

  Enable hypervisor UI at runtime.
  Use -w to also persist the change to the config file.

Usage:
  skywire cli visor hv enable



Flags:
  -w, --persist   write change to config file

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor hv pk

```
Public key of remote hypervisor(s) which are currently connected to

Usage:
  skywire cli visor hv pk



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor hv status

```
Check if hypervisor is enabled

Usage:
  skywire cli visor hv status



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor hv ui

```

  open Hypervisor UI in default browser

Usage:
  skywire cli visor hv ui



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor info

```

  Summary of visor info

Usage:
  skywire cli visor info



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor ip

```

  IP information of network

Usage:
  skywire cli visor ip



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor log

```

  Returns runtime logs from the visor

Usage:
  skywire cli visor log



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor ping

```
Ping commands for testing visor connectivity.

When called with a public key argument, pings that visor directly.

Available subcommands:
  ping <pk>     - Ping a specific visor
  ping test     - Test connectivity to public visors
  ping tree     - Ping visors via transport routes (tree view)
  ping tree2    - Ping visors via transport routes (scrollable TUI)
  ping graph    - Ping visors reachable from this visor
  ping stop-all - Stop all active ping connections

Usage:
  skywire cli visor ping [pk]

Available Commands:
  bandwidth               Test bandwidth to a visor
  graph                   Ping visors across the network by hop level
  stop-all                Stop all active ping connections
  test                    Test the visor with public visors on network
  tree                    Ping visors via transport routes (tree view)
  tree2                   Ping visors via transport routes (scrollable TUI)

Flags:
      --all-servers              Ping through all DMSG servers the remote visor is connected to (only with --dmsg)
      --create-tp                Create a direct transport to the target if none exists
      --dmsg                     Ping over dmsg connection instead of skywire route
      --local-route              Calculate routes locally using cached TPD data instead of querying route finder
      --setup-timeout duration   Timeout for route setup phase (default 30s)
      --show-route               Show the route hops used for the ping
  -s, --size int                 Size of packet, in KB, default is 2KB (default 2)
  -o, --timeout duration         Timeout per ping attempt; fails if exceeded (e.g., 5s, 30s)
      --tp-type string           Transport type to create when using --create-tp (stcpr or sudph) (default "stcpr")
  -t, --tries int                Number of tries (default 1)
      --via-server string        Ping through specific DMSG server (only with --dmsg)

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor ping bandwidth

```
Performs a sustained bandwidth test to measure actual throughput.

Unlike ping which measures latency with small packets, this test:
- Sends and receives data continuously for the specified duration
- Measures real upload and download speeds in KB/s
- Provides progress updates during the test

Use --dmsg to also test over dmsg connection.
Use --dmsg-only to only test over dmsg.

Usage:
  skywire cli visor ping bandwidth <pk>



Flags:
      --dmsg                Also test over dmsg before testing via skywire route
      --dmsg-only           Only test over dmsg (skip skywire route test)
  -d, --duration duration   Duration of the bandwidth test (default 10s)
      --local-route         Calculate routes locally using cached TPD data
  -s, --size int            Packet size in KB (default 32KB) (default 32)

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor ping graph

```
Ping visors reachable from this visor, organized by hop distance.

Level 1: Visors with direct transports to local visor
Level 2: Visors connected to Level 1 visors
Level 3: Visors connected to Level 2 visors
...and so on until no new visors are found.

Uses cached TPD and UT data to build the network graph, then pings
each visor at each level. Skips visors already pinged at earlier levels.

Tree view (--tree) output format:

[39m[39medge[0m[0m
[90m[90m└[0m[0m[90m[90m─[0m[0m[90m[90m─[0m[0m[39m[39medge                                                             tpid                                 -     setup     ping  .....ms      avg[0m[0m

  - edge: visor public key
  - tpid: transport ID (green=stcpr, blue=sudph)
  - -: separator (calc time shown here with --local-route)
  - setup: route setup time in ms
  - ping: ping latencies in ms (one per --tries)
  - avg: average ping latency in ms

  Failures: red text = ping failed, red background = setup/calc failed

Usage:
  skywire cli visor ping graph



Flags:
      --all-servers              ping through all DMSG servers the remote visor is connected to (only with --dmsg or --dmsg-only)
  -m, --cfa int                  update cache files if older than n minutes (default 5)
      --cfd string               DMSG clients cache file location (default "/tmp/dmsg-clients.json")
      --cft string               TPD cache file location (default "/tmp/tpd.json")
      --cfu string               UT cache file location (default "/tmp/ut.json")
      --concurrency int          max concurrent subtree explorations (default 2)
  -c, --continue                 continue on ping failure (don't stop at failed level)
      --dmsg                     also ping over dmsg before pinging via skywire route
      --dmsg-only                only ping over dmsg (skip skywire route ping)
      --dmsgurl string           DMSG discovery URL (default "http://dmsgd.skywire.skycoin.com")
      --dry-run                  show tree structure without pinging (displays dataset size)
      --hop-latency              measure per-hop latency for multi-hop routes (requires --show-route)
      --hops uint                exact hop level to ping (0 = all levels, 1 = direct transports only, 2 = two hops, etc.)
      --local-route              calculate routes locally using cached TPD data instead of querying route finder
      --max-age duration         re-ping entries older than this duration (e.g., 1h, 30m); 0 = never re-ping
  -l, --max-level int            maximum hop level (0 = unlimited)
  -g, --online                   only ping visors marked online in UT
  -O, --output string            output base filename (writes .json and .txt files)
      --redundancy               test all transport types to same visor
      --remake-remote-tp         remake transport on remote side after failure (retry once)
      --remake-tp                remake local transport after removing failed one (retry once)
      --remove-remote-tp         request remote visor to remove transport if route ping fails
      --remove-tp                remove local transport if route ping fails
  -R, --resume                   resume from output file if it exists (continues where left off)
      --retries int              number of retry attempts if ping fails (tree mode only) (default 1)
      --setup-timeout duration   timeout for route setup phase (default 30s)
      --show-route               show the route hops used for the ping
  -s, --size int                 packet size in KB (default 2)
      --start-level int          start pinging from this level (skip earlier levels) (default 1)
  -o, --timeout duration         timeout per ping attempt (default 30s)
      --tpdurl string            transport discovery URL (default "http://tpd.skywire.skycoin.com")
      --tps                      verify/update transports via TPS (tree mode only) (default true)
      --tree                     display results as tree view with per-transport latencies
  -t, --tries int                ping attempts per visor (default 1)
      --uturl string             uptime tracker URL (default "http://ut.skywire.skycoin.com")
  -v, --version string           filter by minimum version (e.g., '1.3.34' matches 1.3.34, 1.3.34+dirty, 1.3.35, etc.)
      --via-server string        ping through specific DMSG server (only with --dmsg or --dmsg-only)

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor ping stop-all

```
Stop all active ping connections and clean up their routes.

Use this to clean up orphaned routes from interrupted ping operations.

Usage:
  skywire cli visor ping stop-all



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor ping test

```

  Creates routes to public visors and measures round-trip latency.

Usage:
  skywire cli visor ping test



Flags:
  -c, --count int   Count of Public Visors for using in test. (default 2)
  -s, --size int    Size of packet, in KB, default is 2KB (default 2)
  -t, --tries int   Number of tries per public visors (default 1)

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor ping tree

```
Ping visors via transport routes, displayed as a tree structure.

This command discovers reachable visors through transports and pings each one,
showing per-transport latencies in a hierarchical tree view.

Output format:
[39m[39mlocal_visor[0m[0m
[90m[90m└[0m[0m[90m[90m─[0m[0m[90m[90m─[0m[0m[39m[39mremote_visor                                           tpid                                 -     setup     ping  .....ms      avg[0m[0m

  - remote_visor: visor public key (first 16 chars)
  - tpid: transport ID (green=stcpr, blue=sudph)
  - setup: route setup time in ms
  - ping: ping latencies in ms (one per --tries)
  - avg: average ping latency in ms

  Colors: red text = ping failed, red background = setup failed

Use --tps to verify transports via Transport Setup Node (fresher data than TPD).
Use --dmsg-only to ping via DMSG servers instead of transport routes.
Use --dmsg to pre-check visor reachability over DMSG before route ping (skips unreachable).
Use --dmsg-all-servers to ping via all DMSG servers (not just first success).

Usage:
  skywire cli visor ping tree



Flags:
      --cdd string               DMSG cache dir ("" to disable) (default "/tmp/dmsgd.skywire.skycoin.com")
      --cdt string               TPD cache dir ("" to disable) (default "/tmp/tpd.skywire.skycoin.com")
      --cdu string               UT cache dir ("" to disable) (default "/tmp/ut.skywire.skycoin.com")
  -m, --cfa int                  update cache files if older than n minutes (default 5)
      --concurrency int          max concurrent ping operations (default 2)
      --continuous               run continuously, re-checking and expanding trees
      --dmsg                     pre-check visor reachability over DMSG before route ping
      --dmsg-all-servers         ping via all DMSG servers (not just first success)
      --dmsg-only                ping via DMSG servers instead of routes
      --dmsgurl string           DMSG discovery URL (default "http://dmsgd.skywire.skycoin.com")
      --dry-run                  show tree structure without pinging
      --hops uint                exact hop level to ping (0 = all levels)
      --max-age duration         re-ping entries older than this duration
  -l, --max-level int            maximum hop level (0 = unlimited)
  -g, --online                   only ping visors marked online in UT
  -O, --output string            output base filename (writes .json file)
      --recheck-age duration     re-ping entries older than this in continuous mode (default 24h0m0s)
      --remake-remote-tp         remake transport on remote side after failure (retry once)
      --remake-tp                remake local transport after removing failed one (retry once)
      --remove-remote-tp         request remote visor to remove transport if route ping fails
      --remove-tp                remove local transport if route ping fails
  -R, --resume                   resume from output file if it exists
      --retries int              retry attempts if ping fails (default 1)
      --setup-timeout duration   timeout for route setup phase (default 30s)
  -s, --size int                 packet size in KB (default 2)
      --testenv                  use test deployment
  -o, --timeout duration         timeout per ping attempt (default 30s)
      --tpdurl string            transport discovery URL (default "http://tpd.skywire.skycoin.com")
      --tps                      verify/update transports via TPS (default: true) (default true)
  -t, --tries int                ping attempts per transport (default 1)
      --uturl string             uptime tracker URL (default "http://ut.skywire.skycoin.com")
  -v, --version string           filter by minimum version

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor ping tree2

```
Ping visors via transport routes with a scrollable terminal UI.

This command uses a Bubble Tea-based TUI that allows scrolling through
results while the ping test is running.

Controls:
  ↑/k, ↓/j     Scroll up/down one line
  PgUp/PgDn    Scroll up/down one page
  Home/End     Go to top/bottom
  q/Ctrl+C     Quit

The display updates live while preserving your scroll position.

Usage:
  skywire cli visor ping tree2



Flags:
  -m, --cfa int                  update cache files if older than n minutes (default 5)
      --cfd string               DMSG clients cache file location (default "/tmp/dmsg-clients.json")
      --cft string               TPD cache file location (default "/tmp/tpd.json")
      --cfu string               UT cache file location (default "/tmp/ut.json")
  -c, --concurrency int          max concurrent ping operations (default 2)
      --continuous               run continuously, re-checking trees
      --dmsg                     pre-check visor reachability over DMSG before route ping
      --dmsg-all-servers         ping via all DMSG servers (not just first success)
      --dmsg-only                ping via DMSG servers instead of routes
      --dmsgurl string           DMSG discovery URL (default "http://dmsgd.skywire.skycoin.com")
      --dry-run                  show tree structure without pinging
      --hops uint                exact hop level to ping (0 = all levels)
      --max-age duration         re-ping entries older than this duration
  -l, --max-level int            maximum hop level (0 = unlimited)
  -g, --online                   only ping visors marked online in UT
  -O, --output string            output base filename (writes .json file)
      --recheck-age duration     re-ping entries older than this in continuous mode (default 24h0m0s)
      --remake-remote-tp         remake transport on remote side after failure (retry once)
      --remake-tp                remake local transport after removing failed one (retry once)
      --remove-remote-tp         request remote visor to remove transport if route ping fails
      --remove-tp                remove local transport if route ping fails
  -R, --resume                   resume from output file if it exists
      --retries int              retry attempts if ping fails (default 1)
      --setup-timeout duration   timeout for route setup phase (default 30s)
  -s, --size int                 packet size in KB (default 2)
  -o, --timeout duration         timeout per ping attempt (default 30s)
      --tpdurl string            transport discovery URL (default "http://tpd.skywire.skycoin.com")
      --tps                      verify/update transports via TPS (default: true) (default true)
  -t, --tries int                ping attempts per transport (default 1)
      --uturl string             uptime tracker URL (default "http://ut.skywire.skycoin.com")
  -v, --version string           filter by minimum version

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor pk

```

  Public key of the visor

Usage:
  skywire cli visor pk



Flags:
  -i, --input string   path of input config file.
  -p, --pkg            read from {/opt/skywire/bin /opt/skywire/local {/opt/skywire/users.db true}}

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor ports

```

  List of all ports used by visor services and apps

Usage:
  skywire cli visor ports



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor proxies

```
Print the runtime state + cumulative stats of the visor-hosted
.dmsg / .skynet resolving proxies. Copy SocksAddr into a browser's
SOCKS5 setting to browse the matched domain; paste WebAddr into curl
for a direct smoke test.

Use "proxies set <kind> on|off" to toggle a resolver at runtime
without editing the config file.

Usage:
  skywire cli visor proxies

Available Commands:
  set                     Toggle a resolving proxy on or off at runtime
  upstream                Set the upstream SOCKS5 proxy for a resolver

Flags:
      --json   emit raw JSON

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor proxies set

```
Toggle the runtime state of an embedded resolving proxy.

Runtime-only: the on-disk config is unchanged, so a visor restart
reverts to the config's 'enable' flag. Use this to experiment with
"what if dmsgweb were on?" without committing to a config edit.

Usage:
  skywire cli visor proxies set <dmsg|skynet> <on|off>



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor proxies upstream

```
Change the upstream SOCKS5 address at runtime. Non-matching
domains are forwarded to this upstream instead of connecting direct.

Use "clear" or "" to remove the upstream (direct connect).

Example chain: browser → dmsgweb (.dmsg) → skynetweb (.skynet) → skysocks (everything else)

  skywire cli visor proxies upstream skynet 127.0.0.1:1080
  skywire cli visor proxies upstream dmsg 127.0.0.1:4446

Usage:
  skywire cli visor proxies upstream <dmsg|skynet> <socks5-addr|clear>



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor ready

```

  Polls the visor and exits once startup is complete.
  Useful in scripts and systemd ExecStartPost.

Usage:
  skywire cli visor ready



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor reinit

```

  Reinitiate modules

Usage:
  skywire cli visor reinit



Flags:
  -m, --module string   target module for reinitiating.

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor reward

```
Fetches reward history from the reward system via the visor's DMSG connection.

Usage:
  skywire cli visor reward



Flags:
  -a, --all         show rewards for all visors connected to the hypervisor
  -d, --days int    number of days of history (default 7)
  -j, --json        output as JSON
  -k, --pk string   visor public key (default: local visor)

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor start

```
start visor

Usage:
  skywire cli visor start



Flags:
  -s, --src   'go run' external commands from the skywire sources

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor user

```
Show the user the visor process is running as

Usage:
  skywire cli visor user



Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor ver

```

  Version and build info

Usage:
  skywire cli visor ver



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli vpn

```
VPN client

Usage:
  skywire cli vpn

Available Commands:
  list                    List servers
  server                  VPN server control
  start                   start the vpn for <public-key>
  status                  vpn client status
  stop                    stop the vpnclient
  ui                      Open VPN UI in default browser
  url                     Show VPN UI URL

Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli vpn list

```
List vpn servers from service discovery
http://sd.skycoin.com/api/services?type=vpn
http://sd.skycoin.com/api/services?type=vpn&country=US

Set cache dir to "" to avoid using cache files
default virtual port of servers: 44

Use --testenv or SKYWIRETEST=1 to use test deployment services.

Usage:
  skywire cli vpn list



Flags:
      --cds string       SD cache dir ("" to disable) (default "/tmp/sd.skycoin.com")
      --cdu string       UT cache dir ("" to disable) (default "/tmp/ut.skywire.skycoin.com")
  -m, --cfa int          update cache files if older than n minutes (default 5)
  -c, --country string   filter by country code
      --json             print output in json
      --maxv string      filter by maximum version (<=)
      --minv string      filter by minimum version (>=)
      --no-dmsg          skip direct DMSG HTTP step
      --no-http          skip direct HTTP fallback step
      --no-rpc           skip visor RPC (DmsgHTTP) step
  -o, --noton            do not filter by online status in UT
      --offline          show only offline servers (red)
  -k, --pk string        check vpn service discovery for public key
  -r, --raw              pretty print json data
  -a, --sdurl string     service discovery url (default "http://sd.skycoin.com")
  -s, --stats            return only a count of the results
      --testenv          use test deployment
  -w, --uturl string     uptime tracker url (default "http://ut.skywire.skycoin.com")
  -v, --version string   filter by version

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli vpn server

```
Control the VPN server application.

The VPN server provides a full VPN tunnel over the Skywire network.
Other visors can connect to this VPN using vpn-client.

Note: VPN server is only supported on Linux.

Usage:
  skywire cli vpn server

Available Commands:
  start                   Start the VPN server
  status                  Show VPN server status
  stop                    Stop the VPN server

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli vpn server start

```
Start the VPN server

Usage:
  skywire cli vpn server start



Flags:
      --external           force external launcher
      --internal           force internal launcher
  -i, --netifc string      network interface for VPN traffic
      --port uint16        routing port for communication between app and visor
      --secure             forbid connections from clients to server local network (default true)
  -w, --whitelist string   comma-separated list of public keys allowed to connect (empty = allow all)

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli vpn server status

```
Show VPN server status

Usage:
  skywire cli vpn server status



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli vpn server stop

```
Stop the VPN server

Usage:
  skywire cli vpn server stop



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli vpn start

```
start the vpn for <public-key>

Usage:
  skywire cli vpn start <public-key>



Flags:
      --existing-tp    only use existing transports, don't create new ones
      --external       force external launcher
      --geoip string   server public key (default "http://ip.skycoin.com")
      --internal       force internal launcher
      --local-route    calculate routes locally instead of using route finder
  -k, --pk string      server public key
  -t, --timeout int    starting timeout value in second

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli vpn status

```
vpn client status

Usage:
  skywire cli vpn status



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli vpn stop

```
stop the vpnclient

Usage:
  skywire cli vpn stop



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli vpn ui

```
Open VPN UI in default browser

Usage:
  skywire cli vpn ui



Flags:
  -c, --config string   config path
  -p, --pkg             use package config path: /opt/skywire

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli vpn url

```
Show VPN UI URL

Usage:
  skywire cli vpn url



Flags:
  -c, --config string   config path
  -p, --pkg             use package config path: /opt/skywire

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

## skywire completion

```
Generate the autocompletion script for skywire for the specified shell.
See each sub-command's help for details on how to use the generated script.

Usage:
  skywire completion

Available Commands:
  bash                    Generate the autocompletion script for bash
  fish                    Generate the autocompletion script for fish
  powershell              Generate the autocompletion script for powershell
  zsh                     Generate the autocompletion script for zsh
```

### skywire completion bash

```
Generate the autocompletion script for the bash shell.

This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

	source <(skywire completion bash)

To load completions for every new session, execute once:

#### Linux:

	skywire completion bash > /etc/bash_completion.d/skywire

#### macOS:

	skywire completion bash > $(brew --prefix)/etc/bash_completion.d/skywire

You will need to start a new shell for this setup to take effect.

Usage:
  skywire completion bash



Flags:
      --no-descriptions   disable completion descriptions
```

### skywire completion fish

```
Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

	skywire completion fish | source

To load completions for every new session, execute once:

	skywire completion fish > ~/.config/fish/completions/skywire.fish

You will need to start a new shell for this setup to take effect.

Usage:
  skywire completion fish [flags]



Flags:
      --no-descriptions   disable completion descriptions
```

### skywire completion powershell

```
Generate the autocompletion script for powershell.

To load completions in your current shell session:

	skywire completion powershell | Out-String | Invoke-Expression

To load completions for every new session, add the output of the above command
to your powershell profile.

Usage:
  skywire completion powershell [flags]



Flags:
      --no-descriptions   disable completion descriptions
```

### skywire completion zsh

```
Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

	echo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

	source <(skywire completion zsh)

To load completions for every new session, execute once:

#### Linux:

	skywire completion zsh > "${fpath[1]}/_skywire"

#### macOS:

	skywire completion zsh > $(brew --prefix)/share/zsh/site-functions/_skywire

You will need to start a new shell for this setup to take effect.

Usage:
  skywire completion zsh [flags]



Flags:
      --no-descriptions   disable completion descriptions
```

## skywire cxo

```
┌─┐─┐ ┬┌─┐
│  ┌┴┬┘│ │
└─┘┴ └─└─┘
CXO is a P2P content-addressable object distribution system.

Usage:
  skywire cxo

Available Commands:
  cli                     CXO command line interface
  daemon                  Run CXO daemon
```

### skywire cxo cli

```
CXO CLI client for interacting with a running CXO daemon via RPC.

Usage:
  skywire cxo cli

Available Commands:
  connection              Connection management commands
  feed                    Feed management commands
  kv                      Key-value store commands
  root                    Root object commands
  stat                    Show node statistics
  stop                    Stop the CXO daemon
  tcp                     TCP transport commands
  udp                     UDP transport commands

Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
  -e, --exec string      execute command and exit
```

#### skywire cxo cli connection

```
Connection management commands

Usage:
  skywire cxo cli connection

Available Commands:
  list                     List all connections
  list-by-feed             List connections for a specific feed

Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli connection list

```
List all connections

Usage:
  skywire cxo cli connection list



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli connection list-by-feed

```
List connections for a specific feed

Usage:
  skywire cxo cli connection list-by-feed <public-key>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

#### skywire cxo cli feed

```
Feed management commands

Usage:
  skywire cxo cli feed

Available Commands:
  is-sharing              Check if a feed is being shared
  list                    List all shared feeds
  share                   Start sharing a feed
  unshare                 Stop sharing a feed

Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli feed is-sharing

```
Check if a feed is being shared

Usage:
  skywire cxo cli feed is-sharing <public-key>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli feed list

```
List all shared feeds

Usage:
  skywire cxo cli feed list



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli feed share

```
Start sharing a feed

Usage:
  skywire cxo cli feed share <public-key>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli feed unshare

```
Stop sharing a feed

Usage:
  skywire cxo cli feed unshare <public-key>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

#### skywire cxo cli kv

```
Key-value store commands

Usage:
  skywire cxo cli kv

Available Commands:
  create                  Create a new KV store (returns feed pubkey and secret key)
  delete                  Delete a key
  get                     Get a value by key
  list                    List all key-value pairs in a store
  put                     Set a key-value pair

Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli kv create

```
Create a new KV store (returns feed pubkey and secret key)

Usage:
  skywire cxo cli kv create



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli kv delete

```
Delete a key

Usage:
  skywire cxo cli kv delete <feed> <secret> <key>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli kv get

```
Get a value by key

Usage:
  skywire cxo cli kv get <feed> <key>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli kv list

```
List all key-value pairs. Use --json for machine-readable output.

Usage:
  skywire cxo cli kv list <feed>



Flags:
      --json   output as JSON

Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli kv put

```
Set a key-value pair

Usage:
  skywire cxo cli kv put <feed> <secret> <key> <value>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

#### skywire cxo cli root

```
Root object commands

Usage:
  skywire cxo cli root

Available Commands:
  info                    Show info of a Root object
  last                    Show info about last Root of a feed
  tree                    Print tree of a Root object

Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli root info

```
Show info of a Root object

Usage:
  skywire cxo cli root info <public-key> <nonce> <seq>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli root last

```
Show info about last Root of a feed

Usage:
  skywire cxo cli root last <public-key>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli root tree

```
Print tree of a Root object

Usage:
  skywire cxo cli root tree <public-key> <nonce> <seq>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

#### skywire cxo cli stat

```
Show node statistics

Usage:
  skywire cxo cli stat



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

#### skywire cxo cli stop

```
Stop the CXO daemon

Usage:
  skywire cxo cli stop



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

#### skywire cxo cli tcp

```
TCP transport commands

Usage:
  skywire cxo cli tcp

Available Commands:
  address                 Show TCP listening address
  connect                 Connect to a TCP address
  disconnect              Disconnect from a TCP address
  subscribe               Subscribe to a feed via TCP connection
  unsubscribe             Unsubscribe from a feed via TCP connection

Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli tcp address

```
Show TCP listening address

Usage:
  skywire cxo cli tcp address



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli tcp connect

```
Connect to a TCP address

Usage:
  skywire cxo cli tcp connect <address>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli tcp disconnect

```
Disconnect from a TCP address

Usage:
  skywire cxo cli tcp disconnect <address>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli tcp subscribe

```
Subscribe to a feed via TCP connection

Usage:
  skywire cxo cli tcp subscribe <address> <public-key>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli tcp unsubscribe

```
Unsubscribe from a feed via TCP connection

Usage:
  skywire cxo cli tcp unsubscribe <address> <public-key>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

#### skywire cxo cli udp

```
UDP transport commands

Usage:
  skywire cxo cli udp

Available Commands:
  address                 Show UDP listening address
  connect                 Connect to a UDP address
  disconnect              Disconnect from a UDP address
  subscribe               Subscribe to a feed via UDP connection
  unsubscribe             Unsubscribe from a feed via UDP connection

Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli udp address

```
Show UDP listening address

Usage:
  skywire cxo cli udp address



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli udp connect

```
Connect to a UDP address

Usage:
  skywire cxo cli udp connect <address>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli udp disconnect

```
Disconnect from a UDP address

Usage:
  skywire cxo cli udp disconnect <address>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli udp subscribe

```
Subscribe to a feed via UDP connection

Usage:
  skywire cxo cli udp subscribe <address> <public-key>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

##### skywire cxo cli udp unsubscribe

```
Unsubscribe from a feed via UDP connection

Usage:
  skywire cxo cli udp unsubscribe <address> <public-key>



Global Flags:
  -a, --address string   RPC address to connect to (default "[::]:8871")
```

### skywire cxo daemon

```
CXO object distribution daemon. Listens for connections, replicates CX objects.

Usage:
  skywire cxo daemon



Flags:
      --data-dir string                 data directory (default "/home/d0mo/.skycoin/cxo")
      --debug                           print debug logs
      --log-prefix string               log prefix (default "[node] ")
      --max-connections int             max connections, incoming and outgoing, tcp and udp (default 1000000)
      --max-filling-time duration       max time to fill a Root (default 10m0s)
      --max-heads int                   max heads of a feed allowed (default 10)
      --mem-db                          use in-memory database
      --public                          public server
      --rpc string                      RPC listening address (default ":8871")
      --tcp string                      TCP listening address (default ":8870")
      --tcp-pings duration              pings interval of TCP connections (default 1m58s)
      --tcp-response-timeout duration   response timeout of TCP connections (default 59s)
      --udp string                      UDP listening address
      --udp-pings duration              pings interval of UDP connections
      --udp-response-timeout duration   response timeout of UDP connections (default 59s)
```

## skywire dmsg

```
┌┬┐┌┬┐┌─┐┌─┐
 │││││└─┐│ ┬
─┴┘┴ ┴└─┘└─┘
v1.3.46-0.20260418190307-6747d8d73adf
built with go1.26.1

Usage:
  skywire dmsg

Available Commands:
  conf                    dmsg deployment servers config
  curl                    DMSG curl utility
  disc                    DMSG Discovery Server
  http                    DMSG http file server
  ip                      DMSG IP utility
  pty                     DMSG pseudoterminal (pty)
  self-ping               DMSG self-ping: dial own PK through a specific server
  server                  DMSG Server
  socks                   DMSG socks5 proxy server & client
  web                     DMSG resolving proxy & browser client

Flags:
  -b, --bv          print runtime/debug.BuildInfo.Main.Version
  -d, --info        print runtime/debug.BuildInfo
      --with-kill   force exit after 3 interrupt signals (default true)
```

### skywire dmsg conf

```
print the dmsg servers from the dmsghttp-config

Usage:
  skywire dmsg conf

Available Commands:
  gen-keys                Generate a new dmsg keypair
  verify-keys             Derive and print the public key from a secret key

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg conf gen-keys

```
Generate a new dmsg keypair

Usage:
  skywire dmsg conf gen-keys



Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg conf verify-keys

```
Derives the public key from the given secret key. Use to verify PK/SK pairs in config files.

Usage:
  skywire dmsg conf verify-keys [secret-key]



Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

### skywire dmsg curl

```
┌┬┐┌┬┐┌─┐┌─┐┌─┐┬ ┬┬─┐┬  
 │││││└─┐│ ┬│  │ │├┬┘│  
─┴┘┴ ┴└─┘└─┘└─┘└─┘┴└─┴─┘
	DMSG curl utility

Usage:
  skywire dmsg curl



Flags:
  -Z, --http               use regular http to connect to DMSG Discovery
  -B, --direct             use dmsg-direct client & don't connect to DMSG Discovery
  -U, --disc-url string    DMSG Discovery URL[0m
                            (default "http://dmsgd.skywire.skycoin.com")
  -A, --disc-addr string   DMSG Discovery dmsg address[0m
                            (default "dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80")
  -D, --dmsgconf string    dmsghttp-config path
  -e, --sess int           number of DMSG Servers to connect to[0m
                            (default 2)
  -S, --srv pk@ip:port     connect via specific dmsg server pk@ip:port[0m
                           
  -p, --proxy string       connect to DMSG via proxy (i.e. '127.0.0.1:1080')
  -l, --loglvl string      [ debug | warn | error | fatal | panic | trace | info ][0m
                            (default "fatal")
  -d, --data string        dmsghttp POST data
  -o, --out string         output filepath
  -r, --replace            replace existing file with new downloaded
  -t, --try int            download attempts (0 unlimits)[0m
                            (default 1)
  -w, --wait int           time to wait between requests
  -a, --agent AGENT        identify as AGENT[0m
                            (default "dmsgcurl/v1.3.46-0")
  -s, --sk cipher.SecKey   a random key is generated if unspecified[0m
                            (default 0000000000000000000000000000000000000000000000000000000000000000)

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

### skywire dmsg disc

```

	┌┬┐┌┬┐┌─┐┌─┐  ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
	 │││││└─┐│ ┬───│││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
	─┴┘┴ ┴└─┘└─┘  ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴
DMSG Discovery Server - registers and discovers DMSG clients and servers.

Depends: redis

HTTP Endpoints:
  GET  /health                                Health check
  GET  /dmsg-discovery/entry/{pk}             Get entry by public key
  POST /dmsg-discovery/entry/                 Register/update entry
  POST /dmsg-discovery/entry/{pk}             Register/update entry
  DEL  /dmsg-discovery/entry                  Delete entry
  GET  /dmsg-discovery/entries                All entries
  GET  /dmsg-discovery/visorEntries           All visor entries
  DEL  /dmsg-discovery/deregister             Deregister entry
  GET  /dmsg-discovery/available_servers      Available DMSG servers
  GET  /dmsg-discovery/all_servers            All DMSG servers
  GET  /dmsg-discovery/servers/clients        Clients by all servers
  GET  /dmsg-discovery/server/{pk}/clients    Clients by specific server

Response Examples:

GET /health
[1m{[0m
      [1m[94m"build_info"[0m[1m:[0m [1m{[0m
        [1m[94m"commit"[0m[1m:[0m [32m"6747d8d73adf"[0m[1m,[0m
        [1m[94m"date"[0m[1m:[0m [32m"2026-04-18T19:03:07Z"[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.46-0-6747d8d73adf"[0m
      [1m}[0m[1m,[0m
      [1m[94m"dmsg_address"[0m[1m:[0m [32m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd:80"[0m[1m,[0m
      [1m[94m"dmsg_servers"[0m[1m:[0m [1m[[0m
        [32m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"[0m,
        [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m
      [1m][0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"2024-01-15T10:00:00Z"[0m
    [1m}[0m

GET /dmsg-discovery/entry/{pk} (client entry)
[1m{[0m
      [1m[94m"client"[0m[1m:[0m [1m{[0m
        [1m[94m"delegated_servers"[0m[1m:[0m [1m[[0m
          [32m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"[0m,
          [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m
        [1m][0m
      [1m}[0m[1m,[0m
      [1m[94m"sequence"[0m[1m:[0m [33m1[0m[1m,[0m
      [1m[94m"static"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m,[0m
      [1m[94m"timestamp"[0m[1m:[0m [33m1705315200[0m[1m,[0m
      [1m[94m"version"[0m[1m:[0m [32m"1.0"[0m
    [1m}[0m

GET /dmsg-discovery/entry/{pk} (server entry)
[1m{[0m
      [1m[94m"version"[0m[1m:[0m [32m""[0m[1m,[0m
      [1m[94m"sequence"[0m[1m:[0m [33m0[0m[1m,[0m
      [1m[94m"timestamp"[0m[1m:[0m [33m0[0m[1m,[0m
      [1m[94m"static"[0m[1m:[0m [32m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"[0m[1m,[0m
      [1m[94m"server"[0m[1m:[0m [1m{[0m
        [1m[94m"address"[0m[1m:[0m [32m"139.162.173.101:30082"[0m[1m,[0m
        [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
      [1m}[0m
    [1m}[0m

POST /dmsg-discovery/entry/ (new entry)
[1m{[0m
      [1m[94m"code"[0m[1m:[0m [33m200[0m[1m,[0m
      [1m[94m"message"[0m[1m:[0m [32m"wrote a new entry"[0m
    [1m}[0m

POST /dmsg-discovery/entry/ (update entry)
[1m{[0m
      [1m[94m"code"[0m[1m:[0m [33m200[0m[1m,[0m
      [1m[94m"message"[0m[1m:[0m [32m"wrote new entry iteration"[0m
    [1m}[0m

DEL /dmsg-discovery/entry
[1m{[0m
      [1m[94m"code"[0m[1m:[0m [33m200[0m[1m,[0m
      [1m[94m"message"[0m[1m:[0m [32m"deleted entry"[0m
    [1m}[0m

GET /dmsg-discovery/entries (all client and server entries)
[1m[[0m
      [1m{[0m
        [1m[94m"client"[0m[1m:[0m [1m{[0m
          [1m[94m"delegated_servers"[0m[1m:[0m [1m[[0m
            [32m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"[0m,
            [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m
          [1m][0m
        [1m}[0m[1m,[0m
        [1m[94m"sequence"[0m[1m:[0m [33m1[0m[1m,[0m
        [1m[94m"static"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m,[0m
        [1m[94m"timestamp"[0m[1m:[0m [33m1705315200[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"1.0"[0m
      [1m}[0m,
      [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"sequence"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"timestamp"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"static"[0m[1m:[0m [32m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"139.162.173.101:30082"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
      [1m}[0m,
      [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"sequence"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"timestamp"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"static"[0m[1m:[0m [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"70.121.13.123:9083"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
      [1m}[0m
    [1m][0m

GET /dmsg-discovery/visorEntries (client entries only)
[1m[[0m
      [1m{[0m
        [1m[94m"client"[0m[1m:[0m [1m{[0m
          [1m[94m"delegated_servers"[0m[1m:[0m [1m[[0m
            [32m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"[0m,
            [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m
          [1m][0m
        [1m}[0m[1m,[0m
        [1m[94m"sequence"[0m[1m:[0m [33m1[0m[1m,[0m
        [1m[94m"static"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m,[0m
        [1m[94m"timestamp"[0m[1m:[0m [33m1705315200[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"1.0"[0m
      [1m}[0m
    [1m][0m

GET /dmsg-discovery/available_servers (servers with available_streams > 0)
[1m[[0m
      [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"sequence"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"timestamp"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"static"[0m[1m:[0m [32m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"139.162.173.101:30082"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
      [1m}[0m,
      [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"sequence"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"timestamp"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"static"[0m[1m:[0m [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"70.121.13.123:9083"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
      [1m}[0m
    [1m][0m

GET /dmsg-discovery/all_servers (all server entries)
[1m[[0m
      [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"sequence"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"timestamp"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"static"[0m[1m:[0m [32m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"139.162.173.101:30082"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
      [1m}[0m,
      [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"sequence"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"timestamp"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"static"[0m[1m:[0m [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"70.121.13.123:9083"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
      [1m}[0m
    [1m][0m

GET /dmsg-discovery/servers/clients
[1m{[0m
      [1m[94m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"[0m[1m:[0m [1m[[0m
        [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
        [32m"024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7"[0m
      [1m][0m
    [1m}[0m

GET /dmsg-discovery/server/{pk}/clients
[1m[[0m
      [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
      [32m"024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7"[0m
    [1m][0m

Example:
  skywire cli config gen-keys > dmsgd-config.json
  skywire dmsg disc --sk $(tail -n1 dmsgd-config.json)

Usage:
  skywire dmsg disc



Flags:
  -a, --addr string               address to bind to
                                   (default ":9090")
      --auth string               auth passphrase as simple auth for official dmsg servers registration
      --dmsg-server-type string   type of dmsg server on dmsghttp handler
      --dmsgPort uint16           dmsg port value
                                   (default 80)
      --enable-load-testing       enable load testing
      --entry-timeout duration    client discovery entry TTL (0 to disable)
                                   (default 1h0m0s)
      --keyfile string            path to file containing secret key (auto-generated if missing)
                                  
  -m, --metrics string            address to serve metrics API from
      --mode string               listener mode: http|dual (dmsg-only is rejected — dmsg-servers reach this service over HTTP)
      --official-servers string   list of official dmsg servers keys separated by comma
      --pprofaddr string          pprof http port (default "localhost:6060")
      --pprofmode string          [ cpu | mem | mutex | block | trace | http ]
      --redis string              connections string for a redis store
                                   (default "redis://localhost:6379")
      --sk cipher.SecKey          dmsg secret key
                                   (default 0000000000000000000000000000000000000000000000000000000000000000)
      --syslog string             address in which to dial to syslog server
      --syslog-lvl string         minimum log level to report (default "debug")
      --syslog-net string         network in which to dial to syslog server (default "udp")
      --tag string                tag used for logging and metrics (default "dmsg_disc")
      --test-environment          distinguished between prod and test environment
  -t, --test-mode                 in testing mode
      --whitelist-keys string     list of whitelisted keys of network monitor used for deregistration

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

### skywire dmsg http

```
┌┬┐┌┬┐┌─┐┌─┐┬ ┬┌┬┐┌┬┐┌─┐
 │││││└─┐│ ┬├─┤ │  │ ├─┘
─┴┘┴ ┴└─┘└─┘┴ ┴ ┴  ┴ ┴  
	DMSG http file server

Usage:
  skywire dmsg http



Flags:
  -Z, --http               use regular http to connect to DMSG Discovery
  -B, --direct             use dmsg-direct client & don't connect to DMSG Discovery
  -U, --disc-url string    DMSG Discovery URL[0m
                            (default "http://dmsgd.skywire.skycoin.com")
  -A, --disc-addr string   DMSG Discovery dmsg address[0m
                            (default "dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80")
  -D, --dmsgconf string    dmsghttp-config path
  -e, --sess int           number of DMSG Servers to connect to[0m
                            (default 2)
  -S, --srv pk@ip:port     connect via specific dmsg server pk@ip:port[0m
                           
  -p, --proxy string       connect to DMSG via proxy (i.e. '127.0.0.1:1080')
  -l, --loglvl string      [ debug | warn | error | fatal | panic | trace | info ][0m
                            (default "debug")
  -r, --dir string         local dir to serve via dmsghttp[0m
                            (default ".")
  -d, --port uint          DMSG port to serve from[0m
                            (default 80)
  -w, --wl strings         whitelist keys to access server, comma separated
  -s, --sk cipher.SecKey   a random key is generated if unspecified[0m
                            (default 0000000000000000000000000000000000000000000000000000000000000000)
      --pprofmode string   [ cpu | mem | mutex | block | trace | http ]
      --pprofaddr string   pprof http port (default "localhost:6060")

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

### skywire dmsg ip

```
┌┬┐┌┬┐┌─┐┌─┐┬┌─┐
 │││││└─┐│ ┬│├─┘
─┴┘┴ ┴└─┘└─┘┴┴  
	DMSG IP utility

Usage:
  skywire dmsg ip



Flags:
  -c, --dmsg-disc string   dmsg discovery url[0m
                            (default "dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80")
  -F, --dmsgconf string    dmsghttp-config path
  -z, --http               use regular http to connect to dmsg discovery
  -l, --loglvl string      [ debug | warn | error | fatal | panic | trace | info ][0m
                            (default "fatal")
  -p, --proxy string       connect to dmsg via proxy (i.e. '127.0.0.1:1080')
  -e, --sess int           number of dmsg servers to connect to[0m
                            (default 1)
  -s, --sk cipher.SecKey   a random key is generated if unspecified
                           [0m (default 0000000000000000000000000000000000000000000000000000000000000000)
  -d, --srv strings        dmsg server public keys

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

### skywire dmsg pty

```

	┌─┐┌┬┐┬ ┬
	├─┘ │ └┬┘
	┴   ┴  ┴
DMSG pseudoterminal (pty)

Usage:
  skywire dmsg pty

Available Commands:
  cli                     DMSG pseudoterminal command line interface
  host                    DMSG host for pseudoterminal command line interface
  ui                      DMSG pseudoterminal GUI

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg pty cli

```
┌┬┐┌┬┐┌─┐┌─┐┌─┐┌┬┐┬ ┬   ┌─┐┬  ┬
 │││││└─┐│ ┬├─┘ │ └┬┘───│  │  │
─┴┘┴ ┴└─┘└─┘┴   ┴  ┴    └─┘┴─┘┴
	DMSG pseudoterminal command line interface

Usage:
  skywire dmsg pty cli

Available Commands:
  whitelist                    lists all whitelisted public keys
  whitelist-add                adds public key(s) to the whitelist
  whitelist-remove             removes public key(s) from the whitelist

Flags:
      --addr dmsg.Addr    remote dmsg address of format 'pk:port'
                           If unspecified, the pty will start locally
                           (default 000000000000000000000000000000000000000000000000000000000000000000:~)
  -a, --args strings      command arguments
  -r, --cliaddr string    address to use for dialing to dmsgpty-host (default "/tmp/dmsgpty.sock")
  -n, --clinet string     network to use for dialing to dmsgpty-host (default "unix")
  -c, --cmd string        name of command to run
                           (default "/bin/bash")
  -p, --confpath string   config path (default "config.json")

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

##### skywire dmsg pty cli whitelist

```
lists all whitelisted public keys

Usage:
  skywire dmsg pty cli whitelist



Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

##### skywire dmsg pty cli whitelist-add

```
adds public key(s) to the whitelist

Usage:
  skywire dmsg pty cli whitelist-add <public-key>...



Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

##### skywire dmsg pty cli whitelist-remove

```
removes public key(s) from the whitelist

Usage:
  skywire dmsg pty cli whitelist-remove <public-key>...



Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg pty host

```
┌┬┐┌┬┐┌─┐┌─┐┌─┐┌┬┐┬ ┬   ┬ ┬┌─┐┌─┐┌┬┐
 │││││└─┐│ ┬├─┘ │ └┬┘───├─┤│ │└─┐ │ 
─┴┘┴ ┴└─┘└─┘┴   ┴  ┴    ┴ ┴└─┘└─┘ ┴ 
	DMSG host for pseudoterminal (pty) command line interface

Usage:
  skywire dmsg pty host

Available Commands:
  confgen                 generates config file

Flags:
      --cliaddr string      address used for listening for cli connections (default "/tmp/dmsgpty.sock")
      --clinet string       network used for listening for cli connections (default "unix")
  -c, --confpath string     config path (default "./config.json")
      --confstdin           config will be read from stdin if set
      --dmsgdisc string     dmsg discovery address (default "dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80")
      --dmsgport uint16     dmsg port for listening for remote hosts (default 22)
      --dmsgsessions int    minimum number of dmsg sessions to ensure (default 1)
      --envprefix string    env prefix (default "DMSGPTY")
      --pprofaddr string    pprof http port (default "localhost:6060")
      --pprofmode string    [ cpu | mem | mutex | block | trace | http ]
      --wl cipher.PubKeys   whitelist of the dmsgpty-host (default public keys:
                            )

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

##### skywire dmsg pty host confgen

```
generates config file

Usage:
  skywire dmsg pty host confgen <config.json>



Flags:
      --unsafe   will unsafely write config if set

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg pty ui

```

	┌┬┐┌┬┐┌─┐┌─┐┌─┐┌┬┐┬ ┬   ┬ ┬┬
	 │││││└─┐│ ┬├─┘ │ └┬┘───│ ││
	─┴┘┴ ┴└─┘└─┘┴   ┴  ┴    └─┘┴
  DMSG pseudoterminal GUI

Usage:
  skywire dmsg pty ui



Flags:
      --addr string       network address to serve UI on (default ":8080")
      --arg stringArray   command arguments to include when initiating pty
      --cmd string        command to run when initiating pty (default "/bin/bash")
      --haddr string      dmsgpty host network address (default "/tmp/dmsgpty.sock")
      --hnet string       dmsgpty host network name (default "unix")

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

### skywire dmsg self-ping

```
Creates a temporary dmsg client, connects to the specified dmsg server,
then dials its own public key through that server.

If the noise handshake completes successfully the round-trip latency is printed
and the command exits 0. If anything fails, the error is printed and the
command exits 1.

The --server flag is required and must be in the format pk@ip:port, e.g.:

  skywire dmsg self-ping --server 02a2d4c3...@139.162.173.101:30082

Usage:
  skywire dmsg self-ping



Flags:
      --server pk@ip:port   dmsg server to connect through: pk@ip:port

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

### skywire dmsg server

```

	┌┬┐┌┬┐┌─┐┌─┐   ┌─┐┌─┐┬─┐┬  ┬┌─┐┬─┐
	││││││└─┐│ ┬ ─ └─┐├┤ ├┬┘└┐┌┘├┤ ├┬┘
	─┴┘┴ ┴└─┘└─┘   └─┘└─┘┴└─ └┘ └─┘┴└─
DMSG Server - relays DMSG traffic between clients.

HTTP Endpoints:
  GET  /health     Health check

Example:
  skywire dmsg server config gen -o dmsg-config.json
  skywire dmsg server start dmsg-config.json

Usage:
  skywire dmsg server

Available Commands:
  config                  Generate a dmsg-server config
  dial                    DMSG Dial network test utility
  start                   Start Dmsg Server

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg server config

```
Generate a dmsg-server config

Usage:
  skywire dmsg server config

Available Commands:
  gen                     Generate a config file

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

##### skywire dmsg server config gen

```
Generate a config file

Usage:
  skywire dmsg server config gen



Flags:
  -o, --output string   config output path/name
  -t, --testenv         use test deployment

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg server dial

```
┌┬┐┌┬┐┌─┐┌─┐┌┬┐┬┌─┐┬  
 │││││└─┐│ ┬ │││├─┤│  
─┴┘┴ ┴└─┘└─┘─┴┘┴┴ ┴┴─┘
DMSG Dial network test utility
Test connection to dmsg servers
Test connecting to dmsg client address [<pk>:<port>]

Default mode of operation is dmsghttp:
* Start dmsg-direct client ; connect directly to a dmsg server
* HTTP client is configured with a dmsg HTTP transport provided by the dmsg-direct client
* HTTP client is used to make HTTP GET request to '/health' of dmsg discovery dmsg address
* If the dmsg-discovery is unreachable via the configured http client:
	- Shuffle dmsg servers
	- Re-make dmsg direct clent
	- Reconfigure HTTP client with dmsg HTTP transport provided by the dmsg-direct client
	- Fetch '/health' from dmsg discovery dmsg address [<pk>:<port>]
	- Repeat the previous 4 steps on error / until no error
* Start dmsghttp client
* Connect to dmsg client address (if specified)

'-Z' flag: use plain http to connect to dmsg-discovery
* HTTP client is used to make HTTP GET request to '/health' of dmsg discovery URL
* Start dmsg client
* Connect to dmsg client address (if specified)

'-B' flag: use dmsg direct client
* Start dmsg-direct client
* Connect to dmsg client address (if specified)

Usage:
  skywire dmsg server dial



Flags:
  -B, --direct             use dmsg-direct client & don't connect to DMSG Discovery
  -A, --disc-addr string   DMSG Discovery dmsg address[0m
                            (default "dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80")
  -U, --disc-url string    DMSG Discovery URL[0m
                            (default "http://dmsgd.skywire.skycoin.com")
  -D, --dmsgconf string    dmsghttp-config path
  -Z, --http               use regular http to connect to DMSG Discovery
  -l, --loglvl string      [ debug | warn | error | fatal | panic | trace | info ][0m
                            (default "info")
  -e, --sess int           number of DMSG Servers to connect to[0m
                            (default 2)
  -s, --sk cipher.SecKey   a random key is generated if unspecified
                           [0m (default 0000000000000000000000000000000000000000000000000000000000000000)
  -S, --srv pk@ip:port     connect via specific dmsg server pk@ip:port[0m
                           
  -w, --wait int           wait time in seconds before disconnecting
                           [0m

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg server start

```
Start Dmsg Server

Usage:
  skywire dmsg server start



Flags:
      --auth string         auth passphrase as simple auth for official dmsg servers registration
  -c, --config string       location of config file (STDIN to read from standard input) (default "config.json")
  -m, --metrics string      address to serve metrics API from
      --pprofaddr string    pprof http port[0m (default "localhost:6060")
      --pprofmode string    [ cpu | mem | mutex | block | trace | http ]
      --stdin               whether to read config via stdin
      --syslog string       address in which to dial to syslog server
      --syslog-lvl string   minimum log level to report (default "debug")
      --syslog-net string   network in which to dial to syslog server (default "udp")
      --tag string          tag used for logging and metrics (default "dmsg_srv")

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

### skywire dmsg socks

```
┌┬┐┌┬┐┌─┐┌─┐   ┌─┐┌─┐┌─┐┬┌─┌─┐
 │││││└─┐│ ┬───└─┐│ ││  ├┴┐└─┐
─┴┘┴ ┴└─┘└─┘   └─┘└─┘└─┘┴ ┴└─┘
	DMSG socks5 proxy server & client

Usage:
  skywire dmsg socks

Available Commands:
  client                  socks5 proxy client for dmsg socks5 proxy server
  server                  dmsg socks5 proxy server

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg socks client

```
socks5 proxy client for dmsg socks5 proxy server

Usage:
  skywire dmsg socks client



Flags:
  -D, --dmsg-disc string   dmsg discovery url[0m
                            (default "dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80")
  -F, --dmsgconf string    dmsghttp-config path[0m
                           
  -q, --dport uint16       dmsg port to connect to socks5 server[0m
                            (default 1081)
  -z, --http               use regular http to connect to dmsg discovery[0m
                           
  -k, --pk string          dmsg socks5 proxy server public key to connect to[0m
                           
  -p, --port int           TCP port to serve SOCKS5 proxy locally[0m
                            (default 1081)
      --pprofaddr string   pprof http port (default "localhost:6060")
      --pprofmode string   [ cpu | mem | mutex | block | trace | http ]
  -s, --sk cipher.SecKey   a random key is generated if unspecified[0m
                            (default 0000000000000000000000000000000000000000000000000000000000000000)

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg socks server

```
dmsg socks5 proxy server

Usage:
  skywire dmsg socks server



Flags:
  -D, --dmsg-disc string   dmsg discovery url[0m
                            (default "dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80")
  -F, --dmsgconf string    dmsghttp-config path[0m
                           
  -q, --dport uint16       dmsg port to serve socks5[0m
                            (default 1081)
  -z, --http               use regular http to connect to dmsg discovery[0m
                           
      --pprofaddr string   pprof http port (default "localhost:6060")
      --pprofmode string   [ cpu | mem | mutex | block | trace | http ]
  -s, --sk cipher.SecKey   a random key is generated if unspecified[0m
                            (default 0000000000000000000000000000000000000000000000000000000000000000)
  -w, --wl string          whitelist keys, comma separated[0m

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

### skywire dmsg web

```

	┌┬┐┌┬┐┌─┐┌─┐┬ ┬┌─┐┌┐
	 │││││└─┐│ ┬│││├┤ ├┴┐
	─┴┘┴ ┴└─┘└─┘└┴┘└─┘└─┘
DMSG resolving proxy & browser client - access websites, HTTP & TCP interfaces over DMSG
.conf file may also be specified with
DMSGWEB=/path/to/dmsgweb.conf skywire dmsg web

Usage:
  skywire dmsg web

Available Commands:
  srv                     Serve HTTP or raw TCP from local port over DMSG

Flags:
  -r, --addproxy string    configure additional socks5 proxy for dmsgweb (i.e. 127.0.0.1:1080)[0m
                           
  -B, --direct             use dmsg-direct client & don't connect to DMSG Discovery
  -A, --disc-addr string   DMSG Discovery dmsg address[0m
                            (default "dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80")
  -U, --disc-url string    DMSG Discovery URL[0m
                            (default "http://dmsgd.skywire.skycoin.com")
  -D, --dmsgconf string    dmsghttp-config path
  -E, --envs               show example .conf file[0m
                           
  -f, --filter string      domain suffix to filter[0m
                            (default ".dmsg")
  -Z, --http               use regular http to connect to DMSG Discovery
  -l, --loglvl string      [ debug | warn | error | fatal | panic | trace | info ][0m
                            (default "debug")
  -p, --port uints         port(s) to serve the web application[0m
                            (default [8080])
      --pprofaddr string   pprof http port (default "localhost:6060")
      --pprofmode string   [ cpu | mem | mutex | block | trace | http ]
  -x, --proxy string       connect to DMSG via proxy (i.e. '127.0.0.1:1080')[0m
                           
  -t, --resolve strings    resolve the specified dmsg address:port on the local port & disable proxy[0m
                           
  -c, --rt bools           proxy to local port as raw TCP, comma separated[0m
                            (default [false])
  -e, --sess int           number of DMSG Servers to connect to[0m
                            (default 2)
  -s, --sk cipher.SecKey   a random key is generated if unspecified
                            (default 0000000000000000000000000000000000000000000000000000000000000000)
  -q, --socks uint         port to serve the socks5 proxy[0m
                            (default 4445)
  -S, --srv pk@ip:port     connect via specific dmsg server pk@ip:port[0m

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg web srv

```
DMSG web server - serve HTTP or raw TCP interface from local port over DMSG
	.conf file may also be specified with DMSGWEBSRV=/path/to/dmsgwebsrv.conf skywire dmsg web srv

Usage:
  skywire dmsg web srv



Flags:
  -B, --direct             use dmsg-direct client & don't connect to DMSG Discovery
  -A, --disc-addr string   DMSG Discovery dmsg address[0m
                            (default "dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80")
  -U, --disc-url string    DMSG Discovery URL[0m
                            (default "http://dmsgd.skywire.skycoin.com")
  -D, --dmsgconf string    dmsghttp-config path
  -d, --dport uints        DMSG port(s) to serve[0m
                            (default [80])
  -E, --envs               show example .conf file
  -Z, --http               use regular http to connect to DMSG Discovery
  -l, --loglvl string      [ debug | warn | error | fatal | panic | trace | info ][0m
                            (default "debug")
  -p, --lport uints        local application interface port(s)[0m
                            (default [8086])
      --pprofaddr string   pprof http port (default "localhost:6060")
      --pprofmode string   [ cpu | mem | mutex | block | trace | http ]
  -x, --proxy string       connect to DMSG via proxy (e.g., '127.0.0.1:1080')
  -c, --rt bools           proxy local port as raw TCP, comma separated[0m
                            (default [false])
  -e, --sess int           number of DMSG Servers to connect to[0m
                            (default 2)
  -s, --sk cipher.SecKey   a random key is generated if unspecified[0m
                            (default 0000000000000000000000000000000000000000000000000000000000000000)
  -S, --srv pk@ip:port     connect via specific dmsg server pk@ip:port[0m
                           
  -w, --wl strings         whitelisted keys for DMSG authenticated routes

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

## skywire skycoin

```
┌─┐┬┌─┬ ┬┌─┐┌─┐┬┌┐┌
└─┐├┴┐└┬┘│  │ │││││
└─┘┴ ┴ ┴ └─┘└─┘┴┘└┘
v1.3.46-0.20260418190307-6747d8d73adf
built with go1.26.1

Usage:
  skywire skycoin

Available Commands:
  cli                     skycoin command line interface
  daemon                  skycoin wallet
  explorer                blockchain explorer
  newcoin                 create a new fibercoin
  web                     skycoin thin client web wallet

Flags:
  -b, --bv     print runtime/debug.BuildInfo.Main.Version
  -d, --info   print runtime/debug.BuildInfo
```

### skywire skycoin cli

```
┌─┐┬┌─┬ ┬┌─┐┌─┐┬┌┐┌   ┌─┐┬  ┬
└─┐├┴┐└┬┘│  │ │││││───│  │  │
└─┘┴ ┴ ┴ └─┘└─┘┴┘└┘   └─┘┴─┘┴
skycoin command line interface

ENVIRONMENT VARIABLES:
  RPC_ADDR: Address of RPC node. Must be in scheme://host format. Default "http://127.0.0.1:6420"
  RPC_USER: Username for RPC API, if enabled in the RPC.
  RPC_PASS: Password for RPC API, if enabled in the RPC.
  COIN: Name of the coin. Default "skycoin"
  DATA_DIR: Directory where everything is stored. Default "$HOME/.$COIN/"

Usage:
  skywire skycoin cli

Available Commands:
  addPrivateKey                      Add a private key to wallet
  addressBalance                     Check the balance of specific addresses
  addressGen                         Generate skycoin or bitcoin addresses
  addressOutputs                     Display outputs of specific addresses
  addressTransactions                Show detail for transaction associated with one or more specified addresses
  addresscount                       Get the count of addresses with unspent outputs (coins)
  blocks                             Lists the content of a single block or a range of blocks
  broadcastTransaction               Broadcast a raw transaction to the network
  checkDBDecoding                    Verify the database data encoding
  checkdb                            Verify the database
  createRawTransaction               Create a raw transaction that can be broadcast to the network later
  createRawTransactionV2             Create a raw transaction that can be broadcast to the network later
  decodeRawTransaction               Decode raw transaction
  decryptWallet                      Decrypt a wallet
  distributeGenesis                  Distributes the genesis block coins into the configured distribution addresses
  encodeJsonTransaction              Encode JSON transaction
  encryptWallet                      Encrypt wallet
  fiberAddressGen                    Generate addresses and seeds for a new fiber coin
  halt                               Shut down the running node
  lastBlocks                         Displays the content of the most recently N generated blocks
  listAddresses                      Lists all addresses in a given wallet
  listWallets                        Lists all wallets stored in the wallet directory
  nextAddress                        Derive the next unused address from an xpub key
  pendingTransactions                Get all unconfirmed transactions
  richlist                           Get skycoin richlist
  send                               Send skycoin from a wallet or an address to a recipient address
  showConfig                         Show cli configuration
  showSeed                           Show wallet seed and seed passphrase
  signTransaction                    Sign an unsigned transaction with specific wallet
  status                             Check the status of current Skycoin node
  transaction                        Show detail info of specific transaction
  unusedAddresses                    Find unused addresses in a wallet (zero balance, no transaction history)
  verifyAddress                      Verify a skycoin address
  verifyTransaction                  Verify if the specific transaction is spendable
  verifyXpub                         Verify a bip32 xpub key
  version                            List the current version of Skycoin components
  walletAddAddresses                 Generate additional addresses for a deterministic, bip44 or xpub wallet
  walletBalance                      Check the balance of a wallet
  walletCreate                       Create a new wallet
  walletCreateTemp                   Create a new temporary wallet
  walletHistory                      Display the transaction history of specific wallet. Requires skycoin node rpc.
  walletKeyExport                    Export a specific key from an HD wallet
  walletOutputs                      Display outputs of specific wallet
  walletScanAddresses                Scan addresses ahead for deterministic, bip44 or xpub wallet
```

#### skywire skycoin cli addPrivateKey

```
Add a private key to wallet.

    This method only works on "collection" type wallets.
    Use "skycoin-cli walletCreate -t collection" to create a "collection" type wallet.

    Use caution when using this from your shell. The private key will be recorded
    if your shell's history file, unless you disable the shell history.

    Use caution when using the "-p" command. If you have command
    history enabled your wallet encryption password can be recovered from the
    history log. If you do not include the "-p" option you will be prompted to
    enter your password after you enter your command.

Usage:
  skywire skycoin cli addPrivateKey [wallet] [private key]



Flags:
  -p, --password string   wallet password
```

#### skywire skycoin cli addressBalance

```
Check balance of specific addresses, join multiple addresses with space.
    example: addressBalance "$addr1 $addr2 $addr3"

Usage:
  skywire skycoin cli addressBalance [addresses]


```

#### skywire skycoin cli addressGen

```
Use caution when using the "-p" command. If you have command history enabled
    your wallet encryption password can be recovered from the history log. If you
    do not include the "-p" option you will be prompted to enter your password
    after you enter your command.

Usage:
  skywire skycoin cli addressGen [flags]



Flags:
  -c, --coin string    Coin type. Must be skycoin or bitcoin. If bitcoin, secret keys are in Wallet Import Format instead of hex. (default "skycoin")
  -x, --encrypt        Encrypt the wallet when printing a JSON wallet
  -e, --entropy int    Entropy of the autogenerated bip39 seed, when the seed is not provided. Can be 128 or 256 (default 128)
      --hex            Use hex(sha256sum(rand(1024))) (CSPRNG-generated) as the seed if not seed is not provided
  -i, --hide-secrets   Hide the secret key and seed from the output when printing a JSON wallet file
  -l, --label string   Wallet label to use when printing or writing a wallet file
  -m, --mode string    Output mode. Options are wallet (prints a full JSON wallet), addresses (prints addresses in plain text), secrets (prints secret keys in plain text) (default "wallet")
  -n, --num int        Number of addresses to generate (default 1)
  -s, --seed string    Seed for deterministic key generation. Will use bip39 as the seed if not provided.
      --stream         Stream output: generate and print each address as it's generated (only for addresses/secrets mode)
  -t, --strict-seed    Seed should be a valid bip39 mnemonic seed.
```

#### skywire skycoin cli addressOutputs

```
Display outputs of specific addresses, join multiple addresses with space,
    example: addressOutputs $addr1 $addr2 $addr3

Usage:
  skywire skycoin cli addressOutputs [address list]


```

#### skywire skycoin cli addressTransactions

```
Display transactions for specific addresses, separate multiple addresses with a space,
        example: addressTransactions addr1 addr2 addr3

Usage:
  skywire skycoin cli addressTransactions [address list]


```

#### skywire skycoin cli addresscount

```
Returns the count of all addresses that currently have unspent outputs (coins) associated with them.

Usage:
  skywire skycoin cli addresscount


```

#### skywire skycoin cli blocks

```
Lists the content of a single block or a range of blocks

Usage:
  skywire skycoin cli blocks [starting block or single block seq] [ending block seq]


```

#### skywire skycoin cli broadcastTransaction

```
Broadcast a raw transaction to the network

Usage:
  skywire skycoin cli broadcastTransaction [raw transaction]


```

#### skywire skycoin cli checkDBDecoding

```
Verify the generated binary encoders match the dynamic encoders for database data.
    If no argument is specificed, the default data.db in $HOME/.$COIN/ will be checked.

Usage:
  skywire skycoin cli checkDBDecoding [db path]


```

#### skywire skycoin cli checkdb

```
Checks if the given database file contains valid skycoin blockchain data.
    If no argument is specificed, the default data.db in $HOME/.$COIN/ will be checked.

Usage:
  skywire skycoin cli checkdb [db path]


```

#### skywire skycoin cli createRawTransaction

```
Create a raw transaction that can be broadcast to the network later.

    Note: The [amount] argument is the coins you will spend, with decimal formatting, e.g. 1, 1.001 or 1.000000.

    The [to address] and [amount] arguments can be replaced with the --many/-m or the --csv option.

    Use caution when using the "-p" command. If you have command history enabled
    your wallet encryption password can be recovered from the history log. If you
    do not include the "-p" option you will be prompted to enter your password
    after you enter your command.

Usage:
  skywire skycoin cli createRawTransaction [wallet] [to address] [amount] [flags]



Flags:
  -c, --change-address string   Specify the change address.
                                Defaults to one of the spending addresses (deterministic wallets) or to a new change address (bip44 wallets).
      --csv string              CSV file containing addresses and amounts to send
  -a, --from-address string     From address in wallet
  -j, --json                    Returns the results in JSON format.
  -m, --many string             use JSON string to set multiple receive addresses and coins,
                                example: -m '[{"addr":"$addr1", "coins": "10.2"}, {"addr":"$addr2", "coins": "20"}]'
  -p, --password string         Wallet password
```

#### skywire skycoin cli createRawTransactionV2

```
Create a raw transaction that can be broadcast to the network later.

    Note: The [amount] argument is the coins you will spend, with decimal formatting, e.g. 1, 1.001 or 1.000000.

    The [to address] and [amount] arguments can be replaced with the --csv option.,

    Use caution when using the "-p" command. If you have command history enabled
    your wallet encryption password can be recovered from the history log. If you
    do not include the "-p" option you will be prompted to enter your password
    after you enter your command.

Usage:
  skywire skycoin cli createRawTransactionV2 [wallet] [to address] [amount] [flags]



Flags:
  -c, --change-address string                 Specify the change address.
                                              	Defaults to one of the spending addresses (deterministic wallets) or to a new change address (bip44 wallets).
      --csv string                            CSV file containing addresses and amounts to send
  -a, --from-address string                   From address in wallet
      --hours-selection-mode string           Hours selection mode (default "share")
      --hours-selection-share-factor string   Hour selection share factor (default "0.5")
      --hours-selection-type string           Hours selection type (default "auto")
      --ignore-unconfirmed                    Ignore unconfirmed transactions
  -j, --json                                  Returns the results in JSON format.
  -p, --password string                       Wallet password
      --unsign                                Do not sign the transaction
```

#### skywire skycoin cli decodeRawTransaction

```
Decode raw transaction

Usage:
  skywire skycoin cli decodeRawTransaction [raw transaction]


```

#### skywire skycoin cli decryptWallet

```
Decrypt an encrypted wallet. The decrypted wallet will be written
    on the filesystem in place of the encrypted wallet.

    Use caution when using the "-p" command. If you have command history enabled
    your wallet encryption password can be recovered from the history log. If you
    do not include the "-p" option you will be prompted to enter your password
    after you enter your command.

Usage:
  skywire skycoin cli decryptWallet [wallet] [flags]



Flags:
  -p, --password string   wallet password
```

#### skywire skycoin cli distributeGenesis

```
Distributes the genesis block coins into the configured distribution addresses.

    The genesis block contains a single transaction with a single output that creates all coins
    in existence. Skycoin expects the second block to be a "distribution" transaction, where
    the genesis coins are split into N distribution addresses, each holding an equal amount.

    RPC_ADDR must be set, to communicate with a running Skycoin node.

Usage:
  skywire skycoin cli distributeGenesis [genesis address secret key] [flags]



Flags:
  -s, --genesis-seckey string   Genesis address secret key
```

#### skywire skycoin cli encodeJsonTransaction

```
Encode JSON transaction

Usage:
  skywire skycoin cli encodeJsonTransaction [file path or -]



Flags:
  -f, --fix    Recompute transaction inner and outer hashes
  -j, --json   Returns the results in JSON format.
```

#### skywire skycoin cli encryptWallet

```
Encrypt a decrypted wallet. The encrypted wallet file
    will be written on the filesystem in place of the decrypted wallet.

    Use caution when using the "-p" command. If you have command history enabled
    your wallet encryption password can be recovered from the history log. If you
    do not include the "-p" option you will be prompted to enter your password
    after you enter your command.

Usage:
  skywire skycoin cli encryptWallet [wallet] [flags]



Flags:
  -p, --password string   wallet password
```

#### skywire skycoin cli fiberAddressGen

```
Addresses are written in a format that can be copied into fiber.toml
    for configuring distribution addresses. Addresses along with their seeds are written to a csv file,
    these seeds can be imported into the wallet to access distribution coins.

Usage:
  skywire skycoin cli fiberAddressGen [flags]



Flags:
  -a, --addrs-file string   Output file for the generated addresses in fiber.toml format (default "addresses.txt")
  -e, --entropy int         Entropy of the autogenerated bip39 seeds. Can be 128 or 256 (default 128)
  -n, --num int             Number of addresses to generate (default 100)
  -o, --overwrite           Allow overwriting any existing addrs-file or seeds-file
  -s, --seeds-file string   Output file for the generated addresses and seeds in a csv (default "seeds.csv")
```

#### skywire skycoin cli halt

```
Sends a shutdown request to the running node via the RPC API.

Usage:
  skywire skycoin cli halt


```

#### skywire skycoin cli lastBlocks

```
Displays the content of the most recently N generated blocks

Usage:
  skywire skycoin cli lastBlocks [numberOfBlocks]


```

#### skywire skycoin cli listAddresses

```
Lists all addresses in a given wallet

Usage:
  skywire skycoin cli listAddresses [wallet]


```

#### skywire skycoin cli listWallets

```
Lists all wallets stored in the wallet directory.

    The [wallet dir] argument is optional. If not provided, defaults to $DATA_DIR/wallets

Usage:
  skywire skycoin cli listWallets


```

#### skywire skycoin cli nextAddress

```
Derive child addresses from a bip32 xpub key and return the first unused one.

An unused address has zero balance and no transaction history.
Derivation starts at child index 0 (or the specified start index) and scans
forward until an unused address is found.

Requires a running skycoin node to check address activity.

Examples:
  skycoin cli nextAddress xpub6CJWevR9X57j...
  skycoin cli nextAddress xpub6CJWevR9X57j... --start 10
  skycoin cli nextAddress xpub6CJWevR9X57j... --batch-size 50

Usage:
  skywire skycoin cli nextAddress [xpub key] [flags]



Flags:
      --batch-size int   Number of addresses to check per batch (default 20)
      --start uint32     Child index to start scanning from
```

#### skywire skycoin cli pendingTransactions

```
Get all unconfirmed transactions

Usage:
  skywire skycoin cli pendingTransactions



Flags:
  -v, --verbose   Require the transaction inputs to include the owner address, coins, hours and calculated hours.
                  	The hours are the original hours the output was created with.
                  	The calculated hours are calculated based upon the current system time, and provide an approximate
                  	coin hour value of the output if it were to be confirmed at that instant.
```

#### skywire skycoin cli richlist

```
Returns top N address (default 20) balances (based on unspent outputs). Optionally include distribution addresses (exluded by default).

Usage:
  skywire skycoin cli richlist [top N addresses (20 default)] [include distribution addresses (false default)]


```

#### skywire skycoin cli send

```
Send skycoin from a wallet or an address to a recipient address.

    Note: the [amount] argument is the coins you will spend, 1 coins = 1e6 droplets.

    The [to address] and [amount] arguments can be replaced with the --many/-m option.

    If you are sending from a wallet without specifying an address,
    the transaction will use one or more of the addresses within the wallet.

    Use caution when using the “-p” command. If you have command history enabled
    your wallet encryption password can be recovered from the history log.
    If you do not include the “-p” option you will be prompted to enter your password
    after you enter your command.

Usage:
  skywire skycoin cli send [wallet] [to address] [amount] [flags]



Flags:
  -c, --change-address string   Specify the change address.
                                Defaults to one of the spending addresses (deterministic wallets) or to a new change address (bip44 wallets).
      --csv string              CSV file containing addresses and amounts to send
  -a, --from-address string     From address in wallet
  -j, --json                    Returns the results in JSON format.
  -m, --many string             use JSON string to set multiple receive addresses and coins,
                                example: -m '[{"addr":"$addr1", "coins": "10.2"}, {"addr":"$addr2", "coins": "20"}]'
  -p, --password string         Wallet password
```

#### skywire skycoin cli showConfig

```
Show cli configuration

Usage:
  skywire skycoin cli showConfig


```

#### skywire skycoin cli showSeed

```
Print the seed and seed passphrase from a wallet.

    Use caution when using the "-p" command. If you have command history enabled
    your wallet encryption password can be recovered from the history log. If you
    do not include the "-p" option you will be prompted to enter your password
    after you enter your command.

Usage:
  skywire skycoin cli showSeed [wallet] [flags]



Flags:
  -j, --json              Returns the results in JSON format.
  -p, --password string   Wallet password
```

#### skywire skycoin cli signTransaction

```
Sign an unsigned transaction with specific wallet

Usage:
  skywire skycoin cli signTransaction [wallet] [raw transaction]


```

#### skywire skycoin cli status

```
Check the status of current Skycoin node

Usage:
  skywire skycoin cli status


```

#### skywire skycoin cli transaction

```
Show detail info of specific transaction

Usage:
  skywire skycoin cli transaction [transaction id]


```

#### skywire skycoin cli unusedAddresses

```
Find addresses in a wallet that have never been used.
An unused address has:
- Zero confirmed balance
- No transaction history (never received or sent coins)

This is useful for generating funding requests where address reuse should be avoided.

Examples:
  skycoin cli unusedAddresses myWallet.wlt
  skycoin cli unusedAddresses myWallet.wlt -n 3
  skycoin cli unusedAddresses myWallet.wlt --generate

Usage:
  skywire skycoin cli unusedAddresses [wallet] [flags]



Flags:
  -g, --generate   Generate new addresses if not enough unused addresses exist
  -n, --num int    Maximum number of unused addresses to return (0 = all)
```

#### skywire skycoin cli verifyAddress

```
Verify a skycoin address

Usage:
  skywire skycoin cli verifyAddress [skycoin address]


```

#### skywire skycoin cli verifyTransaction

```
Verify if the specific transaction is spendable

Usage:
  skywire skycoin cli verifyTransaction [encoded transaction]


```

#### skywire skycoin cli verifyXpub

```
Verify a bip32 xpub key

Usage:
  skywire skycoin cli verifyXpub [xpub key]


```

#### skywire skycoin cli version

```
List the current version of Skycoin components

Usage:
  skywire skycoin cli version [flags]



Flags:
  -j, --json   Returns the results in JSON format
```

#### skywire skycoin cli walletAddAddresses

```
Generate additional addresses for a deterministic, bip44 or xpub wallet.
    Addresses are generated according to the wallet type's generation mechanism.

    Warning: if you generate long (over 20) sequences of empty addresses and use
    a later address this can cause the wallet history scanner to miss your addresses,
    if you load the wallet from seed elsewhere. In that case, you'll have to manually
    generate addresses to cover the gap of unused addresses in the sequence.

    BIP44 wallets generate addresses on the external (0'/0) chain by default.
    Use --chain=change to generate on the change (0'/1) chain instead.

    Use caution when using the "-p" command. If you have command
    history enabled your wallet encryption password can be recovered from the
    history log. If you do not include the "-p" option you will be prompted to
    enter your password after you enter your command.

Usage:
  skywire skycoin cli walletAddAddresses [wallet] [flags]



Flags:
  -c, --chain string          BIP44 chain to generate addresses on (external or change) (default "external")
  -j, --json                  Returns the results in JSON format
  -n, --num uint              Number of addresses to generate (default 1)
  -p, --password string       wallet password
      --private-keys string   wallet private keys for collection wallet
```

#### skywire skycoin cli walletBalance

```
Check the balance of a wallet

Usage:
  skywire skycoin cli walletBalance [wallet]


```

#### skywire skycoin cli walletCreate

```
Create a new wallet.

    Use caution when using the "-p" command. If you have command
    history enabled your wallet encryption password can be recovered
    from the history log. If you do not include the "-p" option you will
    be prompted to enter your password after you enter your command.

    All results are returned in JSON format in addition to being written to the specified filename.

Usage:
  skywire skycoin cli walletCreate [label] [flags]



Flags:
      --bip44-coin uint32        BIP44 coin type (default 8000)
  -e, --encrypt                  Create encrypted wallet. (default true)
  -l, --label string             Wallet label used to identify your wallet
  -m, --mnemonic                 A mnemonic seed consisting of 12 dictionary words will be generated
  -n, --num uint                 Number of addresses to generate. (default 1)
  -p, --password string          Wallet password
      --private-keys string      Collection private keys
  -r, --random                   A random alpha numeric seed will be generated.
      --scan uint                Number of addresses to scan ahead for balances. (default 1)
  -s, --seed string              Your seed
      --seed-passphrase string   Seed passphrase (bip44 wallets only)
  -t, --type string              Wallet type. Types are "collection", "deterministic", "bip44" or "xpub" (default "deterministic")
  -w, --wordcount uint           Number of seed words to use for mnemonic. Must be 12, 15, 18, 21 or 24 (default 12)
      --xpub string              xpub key for "xpub" type wallets
```

#### skywire skycoin cli walletCreateTemp

```
Create a new temporary wallet.

    All results are returned in JSON format in addition to being written to the specified filename.

Usage:
  skywire skycoin cli walletCreateTemp [flags]



Flags:
      --bip44-coin uint32     BIP44 coin type (default 8000)
  -l, --label string          Wallet label used to identify your wallet
  -m, --mnemonic              A mnemonic seed consisting of 12 dictionary words will be generated
  -n, --num uint              Number of addresses to generate. (default 1)
      --private-keys string   Collection private keys
  -r, --random                A random alpha numeric seed will be generated.
      --scan uint             Number of addresses to scan ahead for balances. (default 1)
  -s, --seed string           Your seed
  -t, --type string           Wallet type. Types are "collection", "deterministic", "bip44" or "xpub" (default "deterministic")
  -w, --wordcount uint        Number of seed words to use for mnemonic. Must be 12, 15, 18, 21 or 24 (default 12)
      --xpub string           xpub key for "xpub" type wallets
```

#### skywire skycoin cli walletHistory

```
Display the transaction history of specific wallet. Requires skycoin node rpc.

Usage:
  skywire skycoin cli walletHistory [wallet]


```

#### skywire skycoin cli walletKeyExport

```
This command prints the xpub or xprv key for a given
    HDNode in a bip44 wallet. The HDNode path is specified with --path.
    This path is the <account/change> portion of the bip44 path.

    Please make sure that the node has wallet seed API enabled (--enable-api-sets="INSECURE_WALLET_SEED").

    Example: -k xpub --path=0 prints the account 0 xpub
    Example: -k xpub --path=0/0 prints the account 0, external chain xpub
    Example: -k xprv --path=0/1 prints the account 0, change chain xprv
    Example: -k pub --path=0/0/9 prints the account 0, external chain child 9 public key
    Example: -k prv --path=0/1/8 prints the account 0, change chain child 8 private key

    The bip32 path node apostrophe is implicit for the first element of the path.

    Use caution when using the "-p" command. If you have command
    history enabled your wallet encryption password can be recovered
    from the history log. If you do not include the "-p" option you will
    be prompted to enter your password after you enter your command.

Usage:
  skywire skycoin cli walletKeyExport [wallet] [flags]



Flags:
  -k, --key string        key type ("xpub", "xprv", "pub", "prv") (default "xpub")
  -p, --password string   wallet password
      --path string       bip44 account'/change subpath (default "0/0")
```

#### skywire skycoin cli walletOutputs

```
Display outputs of specific wallet

Usage:
  skywire skycoin cli walletOutputs [wallet]


```

#### skywire skycoin cli walletScanAddresses

```
Scan addresses ahead for deterministic, bip44 or xpub wallet.

    The argument of [wallet] could be a wallet file name or a fullpath of the wallet
    file. For example, both foo.wlt and $HOME/.skycoin/wallets/foo.wlt could be resolved.

    Warning: if you generate long (over 20) sequences of empty addresses and use
    a later address this can cause the wallet history scanner to miss your addresses,
    if you load the wallet from seed elsewhere. In that case, you'll have to manually
    generate addresses to cover the gap of unused addresses in the sequence.

    BIP44 wallets generate their addresses on the external (0'/0) chain.

    Use caution when using the "-p" command. If you have command
    history enabled your wallet encryption password can be recovered from the
    history log. If you do not include the "-p" option you will be prompted to
    enter your password after you enter your command.

Usage:
  skywire skycoin cli walletScanAddresses [wallet] [flags]



Flags:
  -j, --json              Returns the results in json format
  -n, --num uint          Number of addresses to scan ahead (default 20)
  -p, --password string   wallet password
```

### skywire skycoin daemon

```
┌─┐┬┌─┬ ┬┌─┐┌─┐┬┌┐┌
└─┐├┴┐└┬┘│  │ │││││
└─┘┴ ┴ ┴ └─┘└─┘┴┘└┘
 skycoin wallet

Environment variables:
  FIBER_TOML             Path to a fiber.toml file to load custom fibercoin configuration.
                         Sets default values before CLI flags are applied; flags override.
  GENESIS                Path to a genesis wallet JSON file (address, pubkey, seckey).
                         Takes precedence over fiber.toml genesis values.
  USER_BURN_FACTOR       Coinhour burn factor for user-created transactions.
  USER_MAX_TXN_SIZE      Maximum transaction size in bytes for user-created transactions.
  USER_MAX_DECIMALS      Maximum decimal places for droplet precision (max 6).

Usage:
  skywire skycoin daemon [flags]



Flags:
      --address string                              IP Address to run application on. Leave empty to default to a public interface
      --bip44-coin uint32                           BIP44 coin type (default 8000)
      --block-publisher                             run the daemon as a block publisher
      --blockchain-public-key string                public key of the blockchain (default "0328c576d3f420e7682058a981173a4b374c7cc5ff55bf394d3cf57059bbe6456a")
      --blockchain-secret-key string                secret key of the blockchain
      --burn-factor-create-block uint               coinhour burn factor applied when creating blocks (default 10)
      --burn-factor-unconfirmed uint                coinhour burn factor applied to unconfirmed transactions (default 10)
      --coin-hours-name string                      display name for coin hours (default "Coin Hours")
      --coin-hours-name-singular string             singular display name for coin hours (default "Coin Hour")
      --coin-hours-ticker string                    ticker symbol for coin hours (default "SCH")
      --coin-name string                            name of the coin (default "skycoin")
      --color-log                                   Add terminal colors to log output (default true)
      --connection-rate duration                    How often to make an outgoing connection (default 5s)
      --custom-peers-file string                    load custom peers from a newline separate list of ip:port in a file. Note that this is different from the peers.json file in the data directory
      --data-dir string                             directory to store app data (defaults to $HOME/.skycoin) (default "$HOME/.skycoin")
      --db-path string                              path of database file
      --db-read-only                                open bolt db in read only mode
      --disable-api-sets string                     disable API set. Options are READ, STATUS, WALLET, TXN, NET_CTRL, INSECURE_WALLET_SEED, STORAGE, EXPLORER. Multiple values should be separated by comma
      --disable-csp                                 disable Content Security Policy in http response
      --disable-csrf                                disable CSRF check
      --disable-default-peers                       disable the hardcoded default peers
      --disable-header-check                        disables the host, origin and referer header checks.
      --disable-incoming                            Don't allow incoming connections
      --disable-networking                          Disable all network activity
      --disable-outgoing                            Don't make outgoing connections
      --disable-pex                                 disable PEX peer discovery
      --display-name string                         display name of the coin (default "Skycoin")
      --download-peerlist                           download a peers.txt from the peerlist URL (default true)
      --enable-all-api-sets                         enable all API sets except deprecated or insecure. Applied before the disable API sets flag.
      --enable-api-sets string                      enable API set. Options are READ, STATUS, WALLET, TXN, NET_CTRL, INSECURE_WALLET_SEED, STORAGE, EXPLORER. Multiple values should be separated by comma (default "READ,TXN")
      --enable-gui                                  Enable GUI
      --explorer-url string                         URL of the block explorer (default "https://explorer.skycoin.com")
      --genesis-address string                      genesis address (default "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6")
      --genesis-signature string                    genesis block signature (default "eb10468d10054d15f2b6f8946cd46797779aa20a7617ceb4be884189f219bc9a164e56a5b9f7bec392a804ff3740210348d73db77a37adb542a8e08d429ac92700")
      --genesis-timestamp uint                      genesis block timestamp (default 1426562704)
      --gui-dir string                              static content directory for the HTML interface
      --host-whitelist string                       Hostnames to whitelist in the Host header check. Only applies when the web interface is bound to localhost.
      --http-prof                                   run the HTTP profiling interface
      --http-prof-host string                       hostname to bind the HTTP profiling interface to (default "localhost:6060")
      --launch-browser                              launch system default webbrowser at client startup
      --legacy-peer-compat                          Allow connections from legacy peers that don't send blockchain pubkey
      --localhost-only                              Run on localhost and only connect to localhost peers
      --log-level string                            Choices are: debug, info, warn, error, fatal, panic (default "INFO")
      --logtofile                                   log to file
      --max-block-size uint                         maximum total size of transactions in a block (default 32768)
      --max-connections int                         Maximum number of total connections allowed (default 128)
      --max-decimals-create-block uint              max number of decimal places applied when creating blocks (default 3)
      --max-decimals-unconfirmed uint               max number of decimal places applied to unconfirmed transactions (default 3)
      --max-default-peer-outgoing-connections int   The maximum default peer outgoing connections allowed (default 2)
      --max-in-msg-len int                          Maximum length of incoming wire messages (default 1048576)
      --max-incoming-connections int                Maximum number of incoming connections allowed (default 120)
      --max-last-blocks-count uint                  Maximum number of blocks to response for API /api/v1/last_blocks (default 256)
      --max-out-msg-len int                         Maximum length of outgoing wire messages (default 262144)
      --max-outgoing-connections int                Maximum number of outgoing connections allowed (default 8)
      --max-txn-size-create-block uint              maximum size of a transaction applied when creating blocks (default 32768)
      --max-txn-size-unconfirmed uint               maximum size of an unconfirmed transaction (default 32768)
      --no-ping-log                                 disable "reply to ping" and "received pong" debug log messages
      --peerlist-size int                           Max number of peers to track in peerlist (default 65535)
      --peerlist-url string                         URL to download peers.txt from (requires peerlist download enabled) (default "https://downloads.skycoin.com/blockchain/peers.txt")
      --port int                                    Port to run application on (default 6000)
      --profile-cpu                                 enable cpu profiling
      --profile-cpu-file string                     where to write the cpu profile file (default "cpu.prof")
      --qr-uri-prefix string                        prefix for QR code URIs (default "skycoin")
      --reset-corrupt-db                            reset the database if corrupted, and continue running instead of exiting
      --storage-dir string                          location of the storage data files
      --ticker string                               coin ticker symbol (e.g., SKY) (default "SKY")
      --user-agent-remark string                    additional remark to include in the user agent sent over the wire protocol
      --verify-db                                   check the database for corruption
      --version                                     show node version
      --version-url string                          URL for version checking (default "https://version.skycoin.com/skycoin/version.txt")
      --wallet-crypto-type string                   wallet encryption type (see default for format options)
                                                     (default "scrypt-chacha20poly1305")
      --wallet-dir string                           location of the wallet files
      --web-interface                               enable the web interface (default true)
      --web-interface-addr string                   addr to serve web interface on (default "127.0.0.1")
      --web-interface-cert string                   skycoind.cert file for web interface HTTPS. If not provided, will autogenerate or use skycoind.cert in data dir
      --web-interface-https                         enable HTTPS for web interface
      --web-interface-key string                    skycoind.key file for web interface HTTPS. If not provided, will autogenerate or use skycoind.key in data dir
      --web-interface-password string               password for the web interface
      --web-interface-plaintext-auth                allow web interface auth without https
      --web-interface-port int                      port to serve web interface on (default 6420)
      --web-interface-username string               username for the web interface
```

### skywire skycoin explorer

```
┌─┐┬┌─┬ ┬┌─┐┌─┐┬┌┐┌   ┌─┐─┐ ┬┌─┐┬  ┌─┐┬─┐┌─┐┬─┐
└─┐├┴┐└┬┘│  │ │││││───├┤ ┌┴┬┘├─┘│  │ │├┬┘├┤ ├┬┘
└─┘┴ ┴ ┴ └─┘└─┘┴┘└┘   └─┘┴ └─┴  ┴─┘└─┘┴└─└─┘┴└─

Usage:
  skywire skycoin explorer



Flags:
  -a, --api-only              Only run the API, don't serve static content
  -f, --files-folder string   Path for the folder with the precompiled front-end files
  -n, --node-addr string      The skycoin node's address[0m
                               (default "http://127.0.0.1:6420")
  -r, --pprofaddr string      pprof http port (default "localhost:6060")
  -q, --pprofmode string      [ cpu | mem | mutex | block | trace | http ]
  -s, --server-host string    The addr:port to bind the explorer web server to[0m
                               (default "127.0.0.1:8001")
  -v, --verify                Run init() checks and quit
```

### skywire skycoin newcoin

```
┌┐┌┌─┐┬ ┬┌─┐┌─┐┬┌┐┌
│││├┤ ││││  │ │││││
┘└┘└─┘└┴┘└─┘└─┘┴┘└┘
create a new fibercoin

Usage:
  skywire skycoin newcoin

Available Commands:
  config                  Print fiber.toml config
  createcoin              Create a new coin from a template file
  templates               Export embedded templates to a directory
```

#### skywire skycoin newcoin config

```
Prints the default fiber.toml configuration file.

This provides a starting point for creating custom fibercoin configurations.
You can redirect the output to a file and then customize it:

Then edit mycoin.toml to customize your fibercoin parameters.

Usage:
  skywire skycoin newcoin config


```

#### skywire skycoin newcoin createcoin

```
Create a new coin from a template file

Usage:
  skywire skycoin newcoin createcoin [flags]



Flags:
  -c, --coin string                      name of the coin to create (default "skycoin")
  -d, --template-dir string              template directory path (uses embedded templates if empty)
  -e, --coin-template-file string        coin template file (importable) (default "coin.template")
  -f, --command-template-file string     command template file (executable) (default "command.template")
  -g, --coin-test-template-file string   coin test template file (default "coin_test.template")
  -i, --params-template-file string      params template file (default "params.template")
  -j, --config-dir string                config directory path (default "./")
  -k, --config-file string               config file path (default "config/fiber.toml")
```

#### skywire skycoin newcoin templates

```
Exports the embedded newcoin templates to a directory for customization.

If no output directory is specified, templates are printed to stdout.
If an output directory is specified, template files are written there.

After exporting, you can edit the templates and use them with createcoin:

    skycoin newcoin templates ./my-templates
    # edit templates in ./my-templates/
    skycoin newcoin createcoin --coin mycoin --template-dir ./my-templates

Usage:
  skywire skycoin newcoin templates [output-dir]


```

### skywire skycoin web

```
┌─┐┬┌─┬ ┬┌─┐┌─┐┬┌┐┌   ┬ ┬┌─┐┌┐ 
└─┐├┴┐└┬┘│  │ │││││───│││├┤ ├┴┐
└─┘┴ ┴ ┴ └─┘└─┘┴┘└┘   └┴┘└─┘└─┘
Thin client web wallet for skycoin and fibercoins.

Usage:
  skywire skycoin web [flags]



Flags:
      --btc-electrum-url string   Electrum server URL (e.g. ssl://electrum.blockstream.info:50002)
      --btc-node-url string       Bitcoin Core RPC URL (e.g. http://user:pass@127.0.0.1:8332)
      --enable-seed-api           Enable the wallet seed API (requires --wallet-dir)
  -g, --gui-dir string            Custom GUI directory (overrides embedded GUI)
  -H, --host string               Host to bind to (default "127.0.0.1")
  -n, --node-url stringArray      Node URL (can be specified multiple times) (default [https://node.skycoin.com])
  -p, --port int                  Port to serve on (default 8001)
  -r, --pprofaddr string          pprof http port (default "localhost:6060")
  -q, --pprofmode string          [ cpu | mem | mutex | block | trace | http ]
  -w, --wallet-dir stringArray    Local wallet directory (e.g. ~/.skycoin/wallets)
```

## skywire svc

```
┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐   ┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐┌─┐
└─┐├┴┐└┬┘││││├┬┘├┤ ───└─┐├┤ ├┬┘└┐┌┘││  ├┤ └─┐
└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘   └─┘└─┘┴└─ └┘ ┴└─┘└─┘└─┘
	Skywire services

Usage:
  skywire svc

Available Commands:
  ar                      Address Resolver Server for skywire
  conf                    print services-config.json file
  confbs                  Config Bootstrap Server for skywire
  ip                      GeoIP service for skywire
  nm                      Network monitor for skywire VPN and Visor.
  rf                      Route Finder Server for skywire
  sd                      Service discovery server
  se                      skywire environment generator
  sn                      Route Setup Node for skywire
  stun                    STUN server for skywire
  tpd                     Transport Discovery Server for skywire
  tps                     Transport setup server for skywire
  ut                      Uptime Tracker Server for skywire
```

### skywire svc ar

```
┌─┐┌┬┐┌┬┐┬─┐┌─┐┌─┐┌─┐   ┬─┐┌─┐┌─┐┌─┐┬  ┬  ┬┌─┐┬─┐
├─┤ ││ ││├┬┘├┤ └─┐└─┐───├┬┘├┤ └─┐│ ││  └┐┌┘├┤ ├┬┘
┴ ┴─┴┘─┴┘┴└─└─┘└─┘└─┘   ┴└─└─┘└─┘└─┘┴─┘ └┘ └─┘┴└─
Address Resolver Server - resolves visor addresses for STCPR/SUDPH connections.

Depends: redis

Production: http://ar.skywire.skycoin.com
            dmsg://03234b2ee4128d1f78c180d06911102906c80795dfe41bd6253f2619c8b6252a02:80
Test:       http://ar.skywire.dev
            dmsg://03234b2ee4128d1f78c180d06911102906c80795dfe41bd6253f2619c8b6252a02:80

HTTP Endpoints:
  GET  /health                  Health check
  POST /bind/stcpr              Bind STCPR address (auth)
  DEL  /bind/stcpr              Unbind STCPR address (auth)
  GET  /resolve/{type}/{pk}     Resolve address by type and PK
  GET  /transports              List transports
  DEL  /deregister/{network}    Deregister from network
  GET  /security/nonces/{pk}    Get nonce for signing

Request/Response Examples:

GET /health
  [1m{[0m
      [1m[94m"build_info"[0m[1m:[0m [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m[1m,[0m
      [1m[94m"dmsg_address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:80"[0m[1m,[0m
      [1m[94m"dmsg_servers"[0m[1m:[0m [1m[[0m
        [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
      [1m][0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"2024-01-15T10:00:00Z"[0m
    [1m}[0m

POST /bind/stcpr (auth)
  Request:  [1m{[0m
      [1m[94m"port"[0m[1m:[0m [33m30178[0m
    [1m}[0m
  Response: 200 OK

DEL /bind/stcpr (auth)
  Response: 200 OK

GET /resolve/stcpr/{pk}
  [1m{[0m
      [1m[94m"addr"[0m[1m:[0m [32m"192.168.1.100:30178"[0m
    [1m}[0m

GET /resolve/sudph/{pk}
  [1m{[0m
      [1m[94m"addr"[0m[1m:[0m [32m"192.168.1.100:30178"[0m[1m,[0m
      [1m[94m"handshake"[0m[1m:[0m [32m"[0m[35m\u003c[0m[32mbase64_handshake_data[0m[35m\u003e[0m[32m"[0m
    [1m}[0m

GET /transports
  [1m{[0m
      [1m[94m"sudph"[0m[1m:[0m [1m[[0m
        [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m
      [1m][0m[1m,[0m
      [1m[94m"stcpr"[0m[1m:[0m [1m[[0m
        [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
        [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
      [1m][0m
    [1m}[0m

DEL /deregister/{network} (NM auth headers: NM-PK, NM-Sign)
  Request:  [1m[[0m
      [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
      [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
    [1m][0m
  Response: 200 OK

GET /security/nonces/{pk}
  [1m{[0m
      [1m[94m"nonce"[0m[1m:[0m [33m12345[0m
    [1m}[0m

Note: the specified UDP port must be accessible from the internet for SUDPH.

Example:
  skywire cli config gen-keys > ar-config.json
  skywire svc ar --addr ":9093" --redis "redis://localhost:6379" --sk $(tail -n1 ar-config.json)

Usage:
  skywire svc ar



Flags:
  -a, --addr string               address to bind to
                                   (default ":9093")
      --dmsg-disc string          url of dmsg discovery
                                   (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string   type of dmsg server on dmsghttp handler
      --dmsgPort uint16           dmsg port value
                                   (default 80)
      --entry-timeout duration    address binding TTL (0 to disable)
                                   (default 5m0s)
      --keyfile string            path to file containing secret key (auto-generated if missing)
                                  
  -l, --loglvl string             [info|error|warn|debug|trace|panic]
                                   (default "info")
  -m, --metrics string            address to bind metrics API to
      --mode string               listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string              address to bind pprof debug server (e.g. localhost:6060)
      --redis string              connections string for a redis store
                                   (default "redis://localhost:6379")
      --redis-pool-size int       redis connection pool size
                                   (default 10)
      --sk cipher.SecKey          dmsg secret key
                                   (default 0000000000000000000000000000000000000000000000000000000000000000)
      --tag string                logging tag
                                   (default "address_resolver")
      --test-environment          distinguished between prod and test environment
  -t, --testing                   enable testing to start without redis
      --udp-addr string           UDP address to bind to for SUDPH
                                   (default ":30178")
      --whitelist-keys string     list of whitelisted keys of network monitor used for deregistration
```

### skywire svc conf

```
print the full services-config.json (HTTP + DMSG endpoints)

Usage:
  skywire svc conf

Available Commands:
  dmsghttp                print DMSG-only deployment config
  http                    print HTTP-only deployment config
```

#### skywire svc conf dmsghttp

```
print the DMSG-only subset of services-config.json (http fields stripped)

Usage:
  skywire svc conf dmsghttp


```

#### skywire svc conf http

```
print the HTTP-only subset of services-config.json (dmsg fields stripped)

Usage:
  skywire svc conf http


```

### skywire svc confbs

```
┌─┐┌─┐┌┐┌┌─┐┬┌─┐   ┌┐ ┌─┐┌─┐┌┬┐┌─┐┌┬┐┬─┐┌─┐┌─┐┌─┐┌─┐┬─┐
│  │ ││││├┤ ││ ┬───├┴┐│ ││ │ │ └─┐ │ ├┬┘├─┤├─┘├─┘├┤ ├┬┘
└─┘└─┘┘└┘└  ┴└─┘   └─┘└─┘└─┘ ┴ └─┘ ┴ ┴└─┴ ┴┴  ┴  └─┘┴└─
Config Bootstrap Server - provides initial configuration for visors.

Production: http://conf.skywire.skycoin.com
Test:       http://conf.skywire.dev

HTTP Endpoints:
  GET  /health     Health check
  GET  /           Bootstrap configuration (services URLs, keys, etc.)
  GET  /dmsghttp   DMSG HTTP configuration

Response Examples (from actual struct types):

GET /health - api.HealthCheckResponse
[1m{[0m
      [1m[94m"build_info"[0m[1m:[0m [1m{[0m
        [1m[94m"commit"[0m[1m:[0m [32m"abc1234"[0m[1m,[0m
        [1m[94m"date"[0m[1m:[0m [32m"2024-01-15T10:30:00Z"[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m[1m,[0m
      [1m[94m"dmsg_address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:80"[0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"2024-01-15T10:00:00Z"[0m
    [1m}[0m

GET / - visorconfig.Services
[1m{[0m
      [1m[94m"address_resolver"[0m[1m:[0m [32m"http://ar.skywire.skycoin.com"[0m[1m,[0m
      [1m[94m"dmsg_discovery"[0m[1m:[0m [32m"http://dmsgd.skywire.skycoin.com"[0m[1m,[0m
      [1m[94m"route_finder"[0m[1m:[0m [32m"http://rf.skywire.skycoin.com"[0m[1m,[0m
      [1m[94m"route_setup_nodes"[0m[1m:[0m [1m[[0m
        [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
        [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
      [1m][0m[1m,[0m
      [1m[94m"service_discovery"[0m[1m:[0m [32m"http://sd.skycoin.com"[0m[1m,[0m
      [1m[94m"stun_servers"[0m[1m:[0m [1m[[0m
        [32m"stun.l.google.com:19302"[0m
      [1m][0m[1m,[0m
      [1m[94m"transport_discovery"[0m[1m:[0m [32m"http://tpd.skywire.skycoin.com"[0m[1m,[0m
      [1m[94m"transport_setup"[0m[1m:[0m [1m[[0m
        [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m
      [1m][0m[1m,[0m
      [1m[94m"uptime_tracker"[0m[1m:[0m [32m"http://ut.skywire.skycoin.com"[0m
    [1m}[0m

Usage:
  skywire svc confbs



Flags:
  -a, --addr string               address to bind to
                                   (default ":9082")
  -c, --config string             stun server list file location
                                   (default "./config.json")
  -D, --dmsg-disc string          url of dmsg-discovery
                                   (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string   type of dmsg server on dmsghttp handler
      --dmsgPort uint16           dmsg port value
                                   (default 80)
  -d, --domain string             the domain of the endpoints
                                   (default "skywire.skycoin.com")
      --keyfile string            path to file containing secret key (auto-generated if missing)
                                  
      --mode string               listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string              address to bind pprof debug server (e.g. localhost:6060)
      --sk cipher.SecKey          dmsg secret key
                                   (default 0000000000000000000000000000000000000000000000000000000000000000)
      --tag string                logging tag
                                   (default "address_resolver")
```

### skywire svc ip

```
┌─┐┌─┐┌─┐┬┌─┐
│ ┬├┤ │ ││├─┘
└─┘└─┘└─┘┴┴  
GeoIP Service - looks up geographic location data for IP addresses.

Uses embedded MaxMind GeoLite2-City database by default.

HTTP Endpoints (API mode):
  GET  /             Lookup IP (from request or ?ip= param)
  GET  /?ip={ip}     Lookup specific IP address

Request/Response Examples:

GET /?ip=8.8.8.8
  [1m{[0m
      [1m[94m"ip_address"[0m[1m:[0m [32m"8.8.8.8"[0m[1m,[0m
      [1m[94m"latitude"[0m[1m:[0m [33m37.751[0m[1m,[0m
      [1m[94m"longitude"[0m[1m:[0m [33m-97.822[0m[1m,[0m
      [1m[94m"postal_code"[0m[1m:[0m [32m""[0m[1m,[0m
      [1m[94m"continent_code"[0m[1m:[0m [32m"NA"[0m[1m,[0m
      [1m[94m"continent_name"[0m[1m:[0m [32m"North America"[0m[1m,[0m
      [1m[94m"country_code"[0m[1m:[0m [32m"US"[0m[1m,[0m
      [1m[94m"country_name"[0m[1m:[0m [32m"United States"[0m[1m,[0m
      [1m[94m"region_code"[0m[1m:[0m [32m""[0m[1m,[0m
      [1m[94m"region_name"[0m[1m:[0m [32m""[0m[1m,[0m
      [1m[94m"city_name"[0m[1m:[0m [32m""[0m[1m,[0m
      [1m[94m"timezone"[0m[1m:[0m [32m"America/Chicago"[0m
    [1m}[0m

CLI: skywire svc ip 8.8.8.8
  (same response as above)

Usage Examples:
  skywire svc ip 8.8.8.8                              # CLI lookup
  skywire svc ip --api --addr ":9093"                 # API server (embedded DB)
  skywire svc ip --api --db ./GeoLite2-City.mmdb      # API server (external DB)

Usage:
  skywire svc ip



Flags:
  -a, --addr string     address to bind to
                         (default ":8080")
      --api             Run as API server
      --db string       Path to GeoLite2-City.mmdb database
  -l, --loglvl string   [info|error|warn|debug|trace|panic]
                         (default "info")
      --pprof string    address to bind pprof debug server (e.g. localhost:6060)
      --tag string      logging tag
                         (default "geoip")
```

### skywire svc nm

```
┌┐┌┌─┐┌┬┐┬ ┬┌─┐┬─┐┬┌─   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
│││├┤  │ ││││ │├┬┘├┴┐───││││ │││││ │ │ │├┬┘
┘└┘└─┘ ┴ └┴┘└─┘┴└─┴ ┴   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─
Network Monitor - monitors network health and deregisters stale services.

HTTP Endpoints:
  GET  /health     Health check
  GET  /status     Network status

Response Examples (from actual struct types):

GET /health - api.HealthCheckResponse
[1m{[0m
      [1m[94m"build_info"[0m[1m:[0m [1m{[0m
        [1m[94m"commit"[0m[1m:[0m [32m"abc1234"[0m[1m,[0m
        [1m[94m"date"[0m[1m:[0m [32m"2024-01-15T10:30:00Z"[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"2024-01-15T10:00:00Z"[0m
    [1m}[0m

GET /status - nm.Status
[1m{[0m
      [1m[94m"last_update"[0m[1m:[0m [32m"2024-01-15T10:30:00Z"[0m[1m,[0m
      [1m[94m"online_visors"[0m[1m:[0m [33m1542[0m[1m,[0m
      [1m[94m"alive_transports"[0m[1m:[0m [33m3256[0m[1m,[0m
      [1m[94m"available_vpn"[0m[1m:[0m [33m128[0m[1m,[0m
      [1m[94m"available_skysocks"[0m[1m:[0m [33m256[0m[1m,[0m
      [1m[94m"available_public_visor"[0m[1m:[0m [33m512[0m[1m,[0m
      [1m[94m"last_cleaning"[0m[1m:[0m [1m{[0m
        [1m[94m"all_dead_entries_cleaned"[0m[1m:[0m [33m15[0m[1m,[0m
        [1m[94m"transport_discovery"[0m[1m:[0m [33m5[0m[1m,[0m
        [1m[94m"address_resolver"[0m[1m:[0m [1m{[0m
          [1m[94m"sudph"[0m[1m:[0m [33m0[0m[1m,[0m
          [1m[94m"stcpr"[0m[1m:[0m [33m0[0m
        [1m}[0m[1m,[0m
        [1m[94m"dmsg_discovery"[0m[1m:[0m [33m3[0m[1m,[0m
        [1m[94m"vpn"[0m[1m:[0m [33m2[0m[1m,[0m
        [1m[94m"skysocks"[0m[1m:[0m [33m3[0m[1m,[0m
        [1m[94m"public_visor"[0m[1m:[0m [33m2[0m
      [1m}[0m
    [1m}[0m

Usage:
  skywire svc nm

Available Commands:
  deregister              Deregister service(s) from service discovery

Flags:
  -a, --addr string          address to bind to
                              (default ":9080")
      --ar-url string        url to address resolver
                              (default "http://ar.skywire.skycoin.com")
  -d, --cleaning-delay int   time for delay between each service cleaning routine
                              (default 75)
      --dmsgd-url string     url to dmsg discovery
                              (default "http://dmsgd.skywire.skycoin.com")
  -l, --loglvl string        [info|error|warn|debug|trace|panic]
                              (default "info")
      --pk string            pk of network monitor
      --pprof string         address to bind pprof debug server (e.g. localhost:6060)
      --sd-url string        url to service discovery
                              (default "http://sd.skycoin.com")
      --sk string            sk of network monitor
      --tag string           logging tag
                              (default "network_monitor")
      --tpd-url string       url to transport discovery
                              (default "http://tpd.skywire.skycoin.com")
      --ut-url string        url to uptime tracker visor data
                              (default "http://ut.skywire.skycoin.com")
```

#### skywire svc nm deregister

```

  Manually deregister one or more public keys from service discovery.

  By default, uses the local visor's RPC to perform deregistration. The visor's
  public key must be whitelisted as a network monitor in service discovery.

  Alternatively, use --sk to provide a secret key directly for signing the
  deregistration request (useful when running without a visor).

  Service types:
    - vpn      : VPN servers
    - visor    : Public visors
    - skysocks : SOCKS5 proxy servers (alias: proxy)

  Examples:
    # Deregister a VPN server using visor RPC (default)
    skywire svc nm deregister --pk <public-key> --type vpn

    # Deregister using a specific visor RPC address
    skywire svc nm deregister --pk <public-key> --type vpn --rpc localhost:3435

    # Deregister multiple keys from all service types
    skywire svc nm deregister --pk <pk1>,<pk2> --all-types

    # Deregister using a secret key directly (without visor)
    skywire svc nm deregister --pk <public-key> --type visor --sk <secret-key>

    # Deregister from a specific service discovery instance (with --sk)
    skywire svc nm deregister --pk <public-key> --type visor --sk <secret-key> --sd-url http://localhost:9098

Usage:
  skywire svc nm deregister



Flags:
  -a, --all-types       deregister from all service types (vpn, visor, skysocks)
  -p, --pk string       public key(s) to deregister (comma-separated for multiple)
  -r, --rpc string      visor RPC address (used when --sk is not provided)
                         (default "localhost:3435")
      --sd-url string   service discovery URL (only used with --sk)
                         (default "http://sd.skycoin.com")
      --sk string       secret key for signing (if not provided, uses visor RPC)
  -t, --type string     service type: vpn, visor, skysocks (or proxy)
```

### skywire svc rf

```
┬─┐┌─┐┬ ┬┌┬┐┌─┐   ┌─┐┬┌┐┌┌┬┐┌─┐┬─┐
├┬┘│ ││ │ │ ├┤ ───├┤ ││││ ││├┤ ├┬┘
┴└─└─┘└─┘ ┴ └─┘   └  ┴┘└┘─┴┘└─┘┴└─
Route Finder Server - finds routes between visors using transport data.

Depends: redis (shares Redis with TPD)

Production: http://rf.skywire.skycoin.com
            dmsg://039d89c5eedfda4a28b0c58b0b643eff949f08e4f68c8357278081d26f5a592d74:80
Test:       http://rf.skywire.dev
            dmsg://039d89c5eedfda4a28b0c58b0b643eff949f08e4f68c8357278081d26f5a592d74:80

HTTP Endpoints:
  GET  /health     Health check
  POST /routes     Find routes between visors

Request/Response Examples:

GET /health
  [1m{[0m
      [1m[94m"build_info"[0m[1m:[0m [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m[1m,[0m
      [1m[94m"dmsg_address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:80"[0m[1m,[0m
      [1m[94m"dmsg_servers"[0m[1m:[0m [1m[[0m
        [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
      [1m][0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"2024-01-15T10:00:00Z"[0m
    [1m}[0m

POST /routes
  Request:  [1m{[0m
      [1m[94m"edges"[0m[1m:[0m [1m[[0m
        [1m[[0m
          [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
          [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
        [1m][0m
      [1m][0m[1m,[0m
      [1m[94m"opts"[0m[1m:[0m [1m{[0m
        [1m[94m"max_hops"[0m[1m:[0m [33m3[0m[1m,[0m
        [1m[94m"min_hops"[0m[1m:[0m [33m0[0m
      [1m}[0m
    [1m}[0m
  Response: [1m{[0m
      [1m[94m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5-03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m[1m:[0m [1m[[0m
        [1m[[0m
          [1m{[0m
            [1m[94m"from"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m,[0m
            [1m[94m"t_id"[0m[1m:[0m [32m"e7a7f1b3c04047f89e12a0a1459b3456"[0m[1m,[0m
            [1m[94m"to"[0m[1m:[0m [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
          [1m}[0m
        [1m][0m
      [1m][0m
    [1m}[0m

Example:
  skywire cli config gen-keys | tee rf-keys.txt
  route-finder --sk $(tail -n1 rf-keys.txt)

Usage:
  skywire svc rf



Flags:
  -a, --addr string               address to bind to
                                   (default ":9092")
  -D, --dmsg-disc string          url of dmsg discovery
                                   (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string   type of dmsg server on dmsghttp handler
      --dmsgPort uint16           dmsg port value
                                   (default 80)
      --keyfile string            path to file containing secret key (auto-generated if missing)
                                  
  -l, --loglvl string             [info|error|warn|debug|trace|panic]
                                   (default "info")
  -m, --metrics string            address to bind metrics API to
      --mode string               listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string              address to bind pprof debug server (e.g. localhost:6060)
      --redis string              connections string for a redis store
                                   (default "redis://localhost:6379")
      --redis-pool-size int       redis connection pool size
                                   (default 10)
      --sk cipher.SecKey          dmsg secret key
                                   (default 0000000000000000000000000000000000000000000000000000000000000000)
      --tag string                logging tag
                                   (default "route_finder")
  -t, --testing                   enable testing to start without redis
```

### skywire svc sd

```
┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐   ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
└─┐├┤ ├┬┘└┐┌┘││  ├┤ ─── │││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
└─┘└─┘┴└─ └┘ ┴└─┘└─┘   ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴ 
Service Discovery Server - registers and discovers services (VPN, proxy, visor).

Depends: redis

Production: http://sd.skycoin.com
            dmsg://0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7:80
Test:       http://sd.skywire.dev
            dmsg://0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7:80

HTTP Endpoints:
  GET  /health                           Health check
  GET  /api/services                     List services (?type=proxy|vpn|visor)
  GET  /api/services/{addr}              Get specific service
  POST /api/services                     Register service (auth)
  DEL  /api/services/{addr}              Delete service (auth)
  DEL  /api/services/deregister/{type}   Deregister by type
  GET  /security/nonces/{pk}             Get nonce for signing

Request/Response Examples:

GET /health
  [1m{[0m
      [1m[94m"build_info"[0m[1m:[0m [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m[1m,[0m
      [1m[94m"dmsg_address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:80"[0m[1m,[0m
      [1m[94m"dmsg_servers"[0m[1m:[0m [1m[[0m
        [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
      [1m][0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"2024-01-15T10:00:00Z"[0m
    [1m}[0m

GET /api/services?type=vpn&version=v1.3&country=US&quantity=10
  [1m[[0m
      [1m{[0m
        [1m[94m"address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:3"[0m[1m,[0m
        [1m[94m"geo"[0m[1m:[0m [1m{[0m
          [1m[94m"country"[0m[1m:[0m [32m"US"[0m[1m,[0m
          [1m[94m"lat"[0m[1m:[0m [33m37.77[0m[1m,[0m
          [1m[94m"lon"[0m[1m:[0m [33m-122.41[0m[1m,[0m
          [1m[94m"region"[0m[1m:[0m [32m"CA"[0m
        [1m}[0m[1m,[0m
        [1m[94m"type"[0m[1m:[0m [32m"vpn"[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m
    [1m][0m

GET /api/services/{addr}?type=vpn
  [1m{[0m
      [1m[94m"address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:3"[0m[1m,[0m
      [1m[94m"geo"[0m[1m:[0m [1m{[0m
        [1m[94m"country"[0m[1m:[0m [32m"US"[0m[1m,[0m
        [1m[94m"lat"[0m[1m:[0m [33m37.77[0m[1m,[0m
        [1m[94m"lon"[0m[1m:[0m [33m-122.41[0m[1m,[0m
        [1m[94m"region"[0m[1m:[0m [32m"CA"[0m
      [1m}[0m[1m,[0m
      [1m[94m"type"[0m[1m:[0m [32m"vpn"[0m[1m,[0m
      [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
    [1m}[0m

POST /api/services (auth)
  Request:  [1m{[0m
      [1m[94m"address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:3"[0m[1m,[0m
      [1m[94m"type"[0m[1m:[0m [32m"vpn"[0m[1m,[0m
      [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
    [1m}[0m
  Response: (same with geo data added)

DEL /api/services/{addr}?type=vpn (auth)
  Response: true

DEL /api/services/deregister/{type} (NM auth headers: NM-PK, NM-Sign)
  Request:  [1m[[0m
      [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
      [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
    [1m][0m
  Response: true

GET /security/nonces/{pk}
  [1m{[0m
      [1m[94m"nonce"[0m[1m:[0m [33m12345[0m
    [1m}[0m

Example:
  skywire cli config gen-keys | tee sd-keys.txt
  service-discovery --sk $(tail -n1 sd-keys.txt)

Usage:
  skywire svc sd



Flags:
  -a, --addr string               address to bind to
                                   (default ":9098")
  -d, --dmsg-disc string          url of dmsg-discovery
                                   (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string   type of dmsg server on dmsghttp handler
      --dmsgPort uint16           dmsg port value
                                   (default 80)
      --entry-timeout duration    client service entry TTL (0 to disable)
                                   (default 5m0s)
      --geoip string              url of geoip service
                                   (default "http://ip.skycoin.com")
      --keyfile string            path to file containing secret key (auto-generated if missing)
                                  
  -m, --metrics string            address to bind metrics API to
      --mode string               listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string              address to bind pprof debug server (e.g. localhost:6060)
  -r, --redis string              connections string for a redis store
                                   (default "redis://localhost:6379")
  -s, --sk cipher.SecKey          dmsg secret key
                                   (default 0000000000000000000000000000000000000000000000000000000000000000)
  -t, --test                      run in test mode and disable auth
  -w, --whitelist-keys string     list of whitelisted keys of network monitor used for deregistration
```

### skywire svc se

```
┌─┐┬ ┬   ┌─┐┌┐┌┬  ┬
└─┐│││───├┤ │││└┐┌┘
└─┘└┴┘   └─┘┘└┘ └┘

Usage:
  skywire svc se

Available Commands:
  dmsg                    Generate config for dmsg-server
  setup                   Generate config for setup node
  visor                   Generate config for skywire-visor

Flags:
  -d, --docker           Environment with dockerized skywire-services
  -l, --local            Environment with skywire-services on localhost
  -n, --network string   Docker network to use
                          (default "SKYNET")
  -p, --public           Environment with public skywire-services
```

#### skywire svc se dmsg

```
Generate config for dmsg-server

Usage:
  skywire svc se dmsg


```

#### skywire svc se setup

```
Generate config for setup node

Usage:
  skywire svc se setup


```

#### skywire svc se visor

```
Generate config for skywire-visor

Usage:
  skywire svc se visor


```

### skywire svc sn

```
┬─┐┌─┐┬ ┬┌┬┐┌─┐   ┌─┐┌─┐┌┬┐┬ ┬┌─┐   ┌┐┌┌─┐┌┬┐┌─┐
├┬┘│ ││ │ │ ├┤ ───└─┐├┤  │ │ │├─┘───││││ │ ││├┤ 
┴└─└─┘└─┘ ┴ └─┘   └─┘└─┘ ┴ └─┘┴     ┘└┘└─┘─┴┘└─┘
Route Setup Node - establishes routes between visors via dmsg RPC.

Listens on dmsg port 36 for route setup requests from visors.

RPC Methods (via dmsg):
  SetupRPCGateway.DialRouteGroup    Establish bidirectional route
  SetupRPCGateway.HealthCheck       Health check

Example Config:
  [1m{[0m
      [1m[94m"public_key"[0m[1m:[0m [32m"000000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"secret_key"[0m[1m:[0m [32m"0000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"dmsg"[0m[1m:[0m [1m{[0m
        [1m[94m"discovery"[0m[1m:[0m [32m"http://dmsgd.skywire.skycoin.com"[0m[1m,[0m
        [1m[94m"sessions_count"[0m[1m:[0m [33m1[0m[1m,[0m
        [1m[94m"servers"[0m[1m:[0m [2mnull[0m[1m,[0m
        [1m[94m"servers_type"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"protocol"[0m[1m:[0m [32m""[0m
      [1m}[0m[1m,[0m
      [1m[94m"transport_discovery"[0m[1m:[0m [32m"http://tpd.skywire.skycoin.com"[0m[1m,[0m
      [1m[94m"log_level"[0m[1m:[0m [32m""[0m
    [1m}[0m

Generate Keys:
  skywire cli config gen-keys | tee sn-keys.txt
  # Line 1: public_key, Line 2: secret_key

Usage:
  skywire svc sn [config.json]
  skywire cli config gen --sn -o sn-config.json
  skywire cli config gen --sn | skywire svc sn -i

Usage:
  skywire svc sn

Available Commands:
  health                  Health check of route setup node

Flags:
  -m, --metrics string     address to bind metrics API to
  -r, --pprofaddr string   pprof http port (default "localhost:6060")
  -q, --pprofmode string   [ http ] pprof mode
  -i, --stdin              read config from STDIN
      --tag string         logging tag
                            (default "setup_node")
```

#### skywire svc sn health

```
Health check of route setup node

Usage:
  skywire svc sn health <pk>


```

### skywire svc stun

```
┌─┐┌┬┐┬ ┬┌┐┌   ┌─┐┌─┐┬─┐┬  ┬┌─┐┬─┐
└─┐ │ │ ││││───└─┐├┤ ├┬┘└┐┌┘├┤ ├┬┘
└─┘ ┴ └─┘┘└┘   └─┘└─┘┴└─ └┘ └─┘┴└─

STUN server implementing RFC 3489 NAT discovery.
Requires two distinct IPs for full NAT type detection.

  skywire svc stun --primary-ip 139.162.160.227 --secondary-ip 172.104.247.120
  skywire svc stun --primary-ip 127.0.0.1 --secondary-ip 127.0.0.2 --port 3478 --alt-port 3479

Usage:
  skywire svc stun



Flags:
      --alt-port int          alternate STUN port
                               (default 3479)
  -l, --loglvl string         [info|error|warn|debug|trace|panic]
                               (default "info")
      --port int              primary STUN port
                               (default 3478)
      --primary-ip string     primary listening IP (required)
      --secondary-ip string   secondary listening IP (required)
      --tag string            logging tag
                               (default "stun")
```

### skywire svc tpd

```
┌┬┐┬─┐┌─┐┌┐┌┌─┐┌─┐┌─┐┬─┐┌┬┐   ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
 │ ├┬┘├─┤│││└─┐├─┘│ │├┬┘ │ ─── │││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
 ┴ ┴└─┴ ┴┘└┘└─┘┴  └─┘┴└─ ┴    ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴ 
Transport Discovery Server - registers and tracks transports between visors.

Depends: redis

Production: http://tpd.skywire.skycoin.com
            dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80
Test:       http://tpd.skywire.dev
            dmsg://02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:80

HTTP Endpoints:
  GET  /health                        Health check
  GET  /all-transports                All registered transports
  GET  /all-transports/stats          Transport statistics
  GET  /all-transports/per-key-stats  Transport counts per public key
  GET  /transports/id:{id}            Transport by ID (auth)
  GET  /transports/edge:{edge}        Transports by edge public key (auth)
  GET  /transports/stats/{edge}       Transport stats for edge
  POST /transports/                   Register transport (auth)
  DEL  /transports/id:{id}            Delete transport (auth)
  DEL  /transports/deregister         Deregister transport
  GET  /bandwidth/transport/{id}      Bandwidth for transport
  GET  /bandwidth/visor/{pk}          Bandwidth for visor
  GET  /uptimes                       Visor uptimes (proxied from UT)
  GET  /security/nonces/{pk}          Get nonce for signing

Request/Response Examples:

GET /health
  [1m{[0m
      [1m[94m"build_info"[0m[1m:[0m [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m[1m,[0m
      [1m[94m"dmsg_address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:80"[0m[1m,[0m
      [1m[94m"dmsg_servers"[0m[1m:[0m [1m[[0m
        [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
      [1m][0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"2024-01-15T10:00:00Z"[0m
    [1m}[0m

GET /all-transports?selfTransports=hide
  [1m[[0m
      [1m{[0m
        [1m[94m"entry"[0m[1m:[0m [1m{[0m
          [1m[94m"edges"[0m[1m:[0m [1m[[0m
            [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
            [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
          [1m][0m[1m,[0m
          [1m[94m"t_id"[0m[1m:[0m [32m"e7a7f1b3c04047f89e12a0a1459b3456"[0m[1m,[0m
          [1m[94m"type"[0m[1m:[0m [32m"stcpr"[0m
        [1m}[0m[1m,[0m
        [1m[94m"latency_ms"[0m[1m:[0m [33m45.2[0m[1m,[0m
        [1m[94m"registered"[0m[1m:[0m [33m1705312800[0m[1m,[0m
        [1m[94m"signatures"[0m[1m:[0m [1m[[0m
          [32m"00000000...00000000"[0m,
          [32m"00000000...00000000"[0m
        [1m][0m
      [1m}[0m
    [1m][0m

GET /all-transports/stats
  [1m{[0m
      [1m[94m"by_type"[0m[1m:[0m [1m{[0m
        [1m[94m"stcpr"[0m[1m:[0m [33m100[0m[1m,[0m
        [1m[94m"sudph"[0m[1m:[0m [33m50[0m
      [1m}[0m[1m,[0m
      [1m[94m"total_transports"[0m[1m:[0m [33m150[0m[1m,[0m
      [1m[94m"unique_visors"[0m[1m:[0m [33m75[0m
    [1m}[0m

GET /all-transports/per-key-stats
  [1m{[0m
      [1m[94m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m:[0m [1m{[0m
        [1m[94m"stcpr"[0m[1m:[0m [33m3[0m[1m,[0m
        [1m[94m"sudph"[0m[1m:[0m [33m2[0m[1m,[0m
        [1m[94m"total"[0m[1m:[0m [33m5[0m
      [1m}[0m
    [1m}[0m

GET /transports/id:{id} (auth)
  [1m{[0m
      [1m[94m"entry"[0m[1m:[0m [1m{[0m
        [1m[94m"edges"[0m[1m:[0m [1m[[0m
          [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
          [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
        [1m][0m[1m,[0m
        [1m[94m"t_id"[0m[1m:[0m [32m"e7a7f1b3c04047f89e12a0a1459b3456"[0m[1m,[0m
        [1m[94m"type"[0m[1m:[0m [32m"stcpr"[0m
      [1m}[0m[1m,[0m
      [1m[94m"registered"[0m[1m:[0m [33m1705312800[0m[1m,[0m
      [1m[94m"signatures"[0m[1m:[0m [1m[[0m
        [32m"00000000...00000000"[0m,
        [32m"00000000...00000000"[0m
      [1m][0m
    [1m}[0m

GET /transports/edge:{pk} (auth)
  [<signed_entry>, ...]

GET /transports/stats/{edge}
  [1m{[0m
      [1m[94m"by_type"[0m[1m:[0m [1m{[0m
        [1m[94m"stcpr"[0m[1m:[0m [33m3[0m[1m,[0m
        [1m[94m"sudph"[0m[1m:[0m [33m2[0m
      [1m}[0m[1m,[0m
      [1m[94m"total"[0m[1m:[0m [33m5[0m
    [1m}[0m

POST /transports/ (auth)
  Request:  [1m[[0m
      [1m{[0m
        [1m[94m"entry"[0m[1m:[0m [1m{[0m
          [1m[94m"edges"[0m[1m:[0m [1m[[0m
            [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
            [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
          [1m][0m[1m,[0m
          [1m[94m"t_id"[0m[1m:[0m [32m"e7a7f1b3c04047f89e12a0a1459b3456"[0m[1m,[0m
          [1m[94m"type"[0m[1m:[0m [32m"stcpr"[0m
        [1m}[0m[1m,[0m
        [1m[94m"signatures"[0m[1m:[0m [1m[[0m
          [32m"00000000...00000000"[0m,
          [32m"00000000...00000000"[0m
        [1m][0m
      [1m}[0m
    [1m][0m
  Response: <same with registered timestamp>

DEL /transports/id:{id} (auth)
  Response: "transport deleted"

DEL /transports/deregister (NM auth headers: NM-PK, NM-Sign)
  Request:  [1m[[0m
      [32m"e7a7f1b3c04047f89e12a0a1459b3456"[0m
    [1m][0m
  Response: 200 OK

GET /bandwidth/transport/{id}?period=daily&limit=7
  [1m[[0m
      [1m{[0m
        [1m[94m"sent_bytes"[0m[1m:[0m [33m1073741824[0m[1m,[0m
        [1m[94m"recv_bytes"[0m[1m:[0m [33m2147483648[0m
      [1m}[0m
    [1m][0m

GET /bandwidth/visor/{pk}?period=daily&limit=7
  [1m{[0m
      [1m[94m"sent_bytes"[0m[1m:[0m [33m5368709120[0m[1m,[0m
      [1m[94m"recv_bytes"[0m[1m:[0m [33m10737418240[0m
    [1m}[0m

GET /uptimes
  [1m[[0m
      [1m{[0m
        [1m[94m"on"[0m[1m:[0m [36mtrue[0m[1m,[0m
        [1m[94m"pk"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m,[0m
        [1m[94m"tp_count"[0m[1m:[0m [33m5[0m
      [1m}[0m
    [1m][0m

GET /security/nonces/{pk}
  [1m{[0m
      [1m[94m"nonce"[0m[1m:[0m [33m12345[0m
    [1m}[0m

Example:
  skywire cli config gen-keys | tee tpd-keys.txt
  transport-discovery --sk $(tail -n1 tpd-keys.txt)

Usage:
  skywire svc tpd



Flags:
  -a, --addr string               address to bind to
                                   (default ":9091")
      --cxo                       enable CXO feed for transport data distribution over DMSG
      --dmsg-disc string          url of dmsg-discovery
                                   (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string   type of dmsg server on dmsghttp handler
      --dmsgPort uint16           dmsg port value
                                   (default 80)
      --entry-timeout duration    transport entry TTL (0 to disable)
                                   (default 5m0s)
      --keyfile string            path to file containing secret key (auto-generated if missing)
                                  
  -l, --loglvl string             [info|error|warn|debug|trace|panic]
                                   (default "info")
  -m, --metrics string            address to bind metrics API to
      --mode string               listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string              address to bind pprof debug server (e.g. localhost:6060)
      --redis string              connections string for a redis store
                                   (default "redis://localhost:6379")
      --redis-pool-size int       redis connection pool size
                                   (default 10)
      --sk cipher.SecKey          dmsg secret key
                                   (default 0000000000000000000000000000000000000000000000000000000000000000)
      --store-data-path string    path for bandwidth backup files
                                   (default "/var/lib/skywire/tpd/bandwidth")
      --tag string                logging tag
                                   (default "transport_discovery")
      --test-environment          distinguished between prod and test environment
  -t, --testing                   enable testing to start without redis
      --whitelist-keys string     list of whitelisted keys of network monitor used for deregistration
```

### skywire svc tps

```
┌┬┐┬─┐┌─┐┌┐┌┌─┐┌─┐┌─┐┬─┐┌┬┐   ┌─┐┌─┐┌┬┐┬ ┬┌─┐
 │ ├┬┘├─┤│││└─┐├─┘│ │├┬┘ │ ───└─┐├┤  │ │ │├─┘
 ┴ ┴└─┴ ┴┘└┘└─┘┴  └─┘┴└─ ┴    └─┘└─┘ ┴ └─┘┴  
Transport Setup Node - remotely manages transports on visors via dmsg RPC.

HTTP Endpoints:
  POST /{pk}/transports    List transports on visor
  POST /add                Add transport between two visors
  POST /remove             Remove transport from visor

Request/Response Examples:

POST /add
  Request:  [1m{[0m
      [1m[94m"from"[0m[1m:[0m [32m"000000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"to"[0m[1m:[0m [32m"000000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"type"[0m[1m:[0m [32m"stcpr"[0m
    [1m}[0m
  Response: {"id": "e7a7f1b3-c040-47f8-9e12-a0a1459b3456", "type": "stcpr", ...}

POST /remove
  Request:  [1m{[0m
      [1m[94m"from"[0m[1m:[0m [32m"000000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"id"[0m[1m:[0m [32m"00000000-0000-0000-0000-000000000000"[0m
    [1m}[0m
  Response: {"success": true}

GET /{pk}/transports
  [1m[[0m
      [1m{[0m
        [1m[94m"edges"[0m[1m:[0m [1m[[0m
          [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
          [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
        [1m][0m[1m,[0m
        [1m[94m"id"[0m[1m:[0m [32m"e7a7f1b3-c040-47f8-9e12-a0a1459b3456"[0m[1m,[0m
        [1m[94m"type"[0m[1m:[0m [32m"stcpr"[0m
      [1m}[0m
    [1m][0m

Example Config:
[1m{[0m
      [1m[94m"public_key"[0m[1m:[0m [32m"000000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"secret_key"[0m[1m:[0m [32m"0000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"port"[0m[1m:[0m [33m8080[0m[1m,[0m
      [1m[94m"dmsg"[0m[1m:[0m [1m{[0m
        [1m[94m"discovery"[0m[1m:[0m [32m"http://dmsgd.skywire.skycoin.com"[0m[1m,[0m
        [1m[94m"sessions_count"[0m[1m:[0m [33m2[0m[1m,[0m
        [1m[94m"servers"[0m[1m:[0m [2mnull[0m[1m,[0m
        [1m[94m"servers_type"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"protocol"[0m[1m:[0m [32m""[0m
      [1m}[0m
    [1m}[0m

Generate Keys:
  skywire cli config gen-keys | tee tps-keys.txt
  # Line 1: public_key, Line 2: secret_key

Usage:
  skywire svc tps

Available Commands:
  add                     add transport to remote visor
  list                    list transports of remote visor
  rm                      remove transport from remote visor

Flags:
  -c, --config string   path to config file
  -l, --loglvl string   [info|error|warn|debug|trace|panic]
                         (default "debug")
```

#### skywire svc tps add

```
add transport to remote visor

Usage:
  skywire svc tps add



Flags:
  -1, --from string   PK to request transport setup
  -2, --to string     other transport edge PK
  -t, --type string   transport type to request creation of [stcpr|sudph|dmsg]
  -p, --pretty        pretty print result
  -z, --addr string   address of the transport setup-node
                       (default "http://127.0.0.1:8080")
```

#### skywire svc tps list

```
list transports of remote visor

Usage:
  skywire svc tps list



Flags:
  -1, --from string   PK to request transport list
  -p, --pretty        pretty print result
  -z, --addr string   address of the transport setup-node
                       (default "http://127.0.0.1:8080")
```

#### skywire svc tps rm

```
remove transport from remote visor

Usage:
  skywire svc tps rm



Flags:
  -1, --from string   PK to request transport takedown
  -i, --tpid string   id of transport to remove
  -p, --pretty        pretty print result
  -z, --addr string   address of the transport setup-node
                       (default "http://127.0.0.1:8080")
```

### skywire svc ut

```
┬ ┬┌─┐┌┬┐┬┌┬┐┌─┐   ┌┬┐┬─┐┌─┐┌─┐┬┌─┌─┐┬─┐
│ │├─┘ │ ││││├┤ ─── │ ├┬┘├─┤│  ├┴┐├┤ ├┬┘
└─┘┴   ┴ ┴┴ ┴└─┘    ┴ ┴└─┴ ┴└─┘┴ ┴└─┘┴└─
Uptime Tracker Server - tracks visor online status and uptime statistics.

Depends: redis, postgres

Production: http://ut.skywire.skycoin.com
            dmsg://022c424caa6239ba7d1d9d8f7dab56cd5ec6ae2ea9ad97bb94ad4b48f62a540d3f:80
Test:       http://ut.skywire.dev
            dmsg://022c424caa6239ba7d1d9d8f7dab56cd5ec6ae2ea9ad97bb94ad4b48f62a540d3f:80

HTTP Endpoints:
  GET  /health                        Health check
  GET  /v4/update                     Visor heartbeat (auth)
  GET  /visors                        All registered visors
  GET  /uptimes                       Visor uptime data (?v=v2 for v2 format)
  GET  /uptime/{pk}                   Uptime for specific visor
  GET  /dashboard                     Dashboard chart data
  GET  /visor-ips                     Visor IP addresses (private API)
  GET  /security/nonces/{pk}          Get nonce for signing

Request/Response Examples:

GET /health
  [1m{[0m
      [1m[94m"build_info"[0m[1m:[0m [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m[1m,[0m
      [1m[94m"dmsg_address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:80"[0m[1m,[0m
      [1m[94m"dmsg_servers"[0m[1m:[0m [1m[[0m
        [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
      [1m][0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"2024-01-15T10:00:00Z"[0m
    [1m}[0m

GET /v4/update (auth)
  Response: 200 OK

GET /visors
  [1m[[0m
      [1m{[0m
        [1m[94m"city"[0m[1m:[0m [32m"New York"[0m[1m,[0m
        [1m[94m"country"[0m[1m:[0m [32m"US"[0m[1m,[0m
        [1m[94m"ip"[0m[1m:[0m [32m"192.168.1.1"[0m[1m,[0m
        [1m[94m"online"[0m[1m:[0m [36mtrue[0m[1m,[0m
        [1m[94m"pk"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m
    [1m][0m

GET /uptimes
  [1m[[0m
      [1m{[0m
        [1m[94m"key"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m,[0m
        [1m[94m"online"[0m[1m:[0m [36mtrue[0m
      [1m}[0m,
      [1m{[0m
        [1m[94m"key"[0m[1m:[0m [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m[1m,[0m
        [1m[94m"online"[0m[1m:[0m [36mfalse[0m
      [1m}[0m
    [1m][0m

GET /uptimes?v=v2
  [1m[[0m
      [1m{[0m
        [1m[94m"pk"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m,[0m
        [1m[94m"on"[0m[1m:[0m [36mtrue[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m[1m,[0m
        [1m[94m"daily"[0m[1m:[0m [1m{[0m
          [1m[94m"2024-01-14"[0m[1m:[0m [32m"100.0"[0m[1m,[0m
          [1m[94m"2024-01-15"[0m[1m:[0m [32m"95.5"[0m
        [1m}[0m
      [1m}[0m
    [1m][0m

GET /uptimes?status=on
  (same as /uptimes, filtered to online visors only)

GET /uptime/{pk}
  [1m{[0m
      [1m[94m"key"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m,[0m
      [1m[94m"online"[0m[1m:[0m [36mtrue[0m
    [1m}[0m

GET /dashboard?length=6
  Response: HTML bar chart of monthly node counts

GET /visor-ips?month=all (private API)
  [1m{[0m
      [1m[94m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m:[0m [32m"192.168.1.1"[0m[1m,[0m
      [1m[94m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m[1m:[0m [32m"10.0.0.1"[0m
    [1m}[0m

GET /security/nonces/{pk}
  [1m{[0m
      [1m[94m"nonce"[0m[1m:[0m [33m12345[0m
    [1m}[0m

Usage:
  skywire svc ut



Flags:
  -a, --addr string              address to bind to
                                  (default ":9096")
      --dmsg-disc string         url of dmsg discovery
                                  (default "http://dmsgd.skywire.skycoin.com")
      --dmsgPort uint16          dmsg port value
                                  (default 80)
      --enable-load-testing      enable load testing
      --geoip string             url of geoip service
                                  (default "http://ip.skycoin.com")
      --keyfile string           path to file containing secret key (auto-generated if missing)
                                 
  -l, --log                      enable request logging
                                  (default true)
  -m, --metrics string           address to bind metrics API to
                                  (default ":2121")
      --mode string              listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pg-host string           host of postgres
                                  (default "localhost")
      --pg-max-open-conn int     maximum open connection of db
                                  (default 60)
      --pg-port string           port of postgres
                                  (default "5432")
      --pprof string             address to bind pprof debug server (e.g. localhost:6060)
  -p, --private-addr string      private address to bind to
                                  (default ":9086")
      --redis string             connections string for a redis store
                                  (default "redis://localhost:6379")
      --redis-pool-size int      redis connection pool size
                                  (default 10)
      --sk cipher.SecKey         dmsg secret key
                                  (default 0000000000000000000000000000000000000000000000000000000000000000)
      --store-data-cutoff int    number of days data store in db
                                  (default 7)
      --store-data-path string   path of db daily data store
                                  (default "/var/lib/skywire/ut/daily")
      --tag string               logging tag
                                  (default "uptime_tracker")
  -t, --testing                  enable testing to start without redis
```

## skywire visor

```
┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐   ┬  ┬┬┌─┐┌─┐┬─┐
└─┐├┴┐└┬┘││││├┬┘├┤ ───└┐┌┘│└─┐│ │├┬┘
└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘    └┘ ┴└─┘└─┘┴└─

Usage:
  skywire visor



Flags:
  -c, --config string   config file to use (default): skywire-config.json
      --systray         run as systray
      --all             show all flags
```

