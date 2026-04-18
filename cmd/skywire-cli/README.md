# Skywire CLI

## Subcommand Tree

```
cli
├── completion
├── config
│   ├── check-pk
│   ├── gen
│   ├── gen-keys
│   ├── parse
│   ├── pk
│   ├── show
│   └── update
│       ├── hv
│       ├── sc
│       ├── ss
│       ├── svc
│       ├── vpnc
│       └── vpns
├── dmsg
│   ├── connect-all
│   ├── curl
│   ├── probe
│   ├── pty
│   │   ├── list
│   │   ├── start
│   │   ├── ui
│   │   └── url
│   ├── sessions
│   └── set-sessions
├── gotop
├── log
│   ├── st
│   └── tp
├── mdisc
│   ├── entry
│   └── servers
├── proxy
│   ├── list
│   ├── route
│   │   ├── add
│   │   └── remove
│   ├── server
│   │   ├── start
│   │   ├── status
│   │   └── stop
│   ├── start
│   ├── status
│   ├── stop
│   └── test
├── pv
├── reward
│   └── rules
├── rewards
│   ├── bot
│   ├── bw-collect
│   ├── loginchain
│   ├── script
│   │   ├── getlogs
│   │   └── reward
│   ├── svc
│   ├── systemd
│   ├── tp-collect
│   └── ui
├── rg
│   └── ls
├── route
│   ├── add
│   │   ├── a
│   │   ├── b
│   │   └── c
│   ├── calc
│   ├── find
│   ├── groups
│   ├── rm
│   └── rsn-stats
├── sd
├── skychat
│   ├── listen
│   └── send
├── skynet
│   ├── curl
│   ├── port
│   │   ├── add
│   │   ├── ls
│   │   └── rm
│   ├── srv
│   │   ├── start
│   │   ├── status
│   │   └── stop
│   ├── start
│   ├── status
│   └── stop
├── survey
├── svc
│   ├── ar
│   ├── dmsgd
│   │   ├── all-servers
│   │   ├── clients
│   │   └── server-clients
│   ├── health
│   ├── nm
│   └── tpd
│       ├── bandwidth
│       ├── bandwidth-tp
│       ├── metrics-tp
│       ├── metrics-visor
│       ├── per-key-stats
│       ├── stats
│       ├── versions
│       ├── versions-pk
│       └── visor-stats
├── tp
│   ├── add
│   │   ├── edge
│   │   └── pv
│   ├── auto
│   ├── disc
│   ├── id
│   ├── metrics
│   ├── net-stats
│   ├── rm
│   ├── sync
│   ├── tpd-health
│   ├── tpd-stats
│   ├── tree
│   ├── uptime
│   ├── v
│   └── viz
├── tps
│   ├── add
│   ├── list
│   └── rm
├── ut
│   ├── mdisc
│   │   └── graph
│   ├── sd
│   │   └── graph
│   └── tpd
│       └── graph
├── util
│   ├── edit
│   ├── got
│   │   ├── dl
│   │   ├── head
│   │   └── req
│   ├── jq
│   └── serve
├── visor
│   ├── app
│   │   ├── arg
│   │   │   ├── autostart
│   │   │   ├── killswitch
│   │   │   ├── netifc
│   │   │   ├── passcode
│   │   │   └── secure
│   │   ├── deregister
│   │   ├── log
│   │   ├── ls
│   │   ├── register
│   │   ├── start
│   │   └── stop
│   ├── dmsg-servers
│   ├── go
│   ├── halt
│   ├── hv
│   │   ├── cpk
│   │   ├── disable
│   │   ├── enable
│   │   ├── pk
│   │   ├── status
│   │   └── ui
│   ├── info
│   ├── ip
│   ├── log
│   ├── ping
│   │   ├── bandwidth
│   │   ├── graph
│   │   ├── stop-all
│   │   ├── test
│   │   ├── tree
│   │   └── tree2
│   ├── pk
│   ├── ports
│   ├── proxies
│   │   ├── set
│   │   └── upstream
│   ├── ready
│   ├── reinit
│   ├── reward
│   ├── start
│   ├── user
│   └── ver
└── vpn
    ├── list
    ├── server
    │   ├── start
    │   ├── status
    │   └── stop
    ├── start
    ├── status
    ├── stop
    ├── ui
    └── url
```

## Command Reference

# skywire cli

# skywire cli

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

## skywire cli completion

```
Generate completion script

Usage:
  skywire cli completion [bash|zsh|fish|powershell]


```

## skywire cli config

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

### skywire cli config check-pk

```
check a skywire public key

Usage:
  skywire cli config check-pk <public-key>


```

### skywire cli config gen

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

### skywire cli config gen-keys

```
generate public / secret keypair

Usage:
  skywire cli config gen-keys


```

### skywire cli config parse

```
check for errors in parsing skywire config

Usage:
  skywire cli config parse <skywire-config.json>


```

### skywire cli config pk

```
derive public key from a secret key

Usage:
  skywire cli config pk <secret-key-hex>


```

### skywire cli config show

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

### skywire cli config update

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

#### skywire cli config update hv

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

#### skywire cli config update sc

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

#### skywire cli config update ss

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

#### skywire cli config update svc

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

#### skywire cli config update vpnc

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

#### skywire cli config update vpns

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

## skywire cli dmsg

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

### skywire cli dmsg connect-all

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

### skywire cli dmsg curl

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

### skywire cli dmsg probe

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

### skywire cli dmsg pty

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

#### skywire cli dmsg pty list

```
List connected visors

Usage:
  skywire cli dmsg pty list



Flags:
      --rpc string   RPC server address (default "localhost:3435")
```

#### skywire cli dmsg pty start

```
Start dmsgpty session

Usage:
  skywire cli dmsg pty start <pk>



Flags:
  -p, --port string   port of remote visor dmsgpty (default "22")
      --rpc string    RPC server address (default "localhost:3435")
```

#### skywire cli dmsg pty ui

```
Open dmsgpty UI in default browser

Usage:
  skywire cli dmsg pty ui



Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to
```

#### skywire cli dmsg pty url

```
Show dmsgpty UI URL

Usage:
  skywire cli dmsg pty url



Flags:
  -i, --input string   read from specified config file
  -p, --pkg            read from /opt/skywire/skywire.json
  -v, --visor string   public key of visor to connect to
```

### skywire cli dmsg sessions

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

### skywire cli dmsg set-sessions

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

## skywire cli gotop

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

## skywire cli log

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

### skywire cli log st

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

### skywire cli log tp

```
display collected transport bandwidth logging

Usage:
  skywire cli log tp



Flags:
  -d, --dir string   path to surveys & transport bandwidth logging
```

## skywire cli mdisc

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

### skywire cli mdisc entry

```
Fetch an entry

Usage:
  skywire cli mdisc entry <visor-public-key>



Flags:
      --url string   specify alternative DMSG discovery url (default "http://dmsgd.skywire.skycoin.com")
```

### skywire cli mdisc servers

```
Fetch available servers

Usage:
  skywire cli mdisc servers



Flags:
      --url string   specify alternative DMSG discovery url (default "http://dmsgd.skywire.skycoin.com")
```

## skywire cli proxy

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

### skywire cli proxy list

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

### skywire cli proxy route

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

#### skywire cli proxy route add

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

#### skywire cli proxy route remove

```
Remove a specific transport's route from the proxy's multiplexed connection.
Traffic is redistributed across remaining routes. Cannot remove the last route.

Usage:
  skywire cli proxy route remove <transport-id>



Global Flags:
      --name string   name of the proxy client app (default "skysocks-client")
      --rpc string    RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli proxy server

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

#### skywire cli proxy server start

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

#### skywire cli proxy server status

```
Show skysocks server status

Usage:
  skywire cli proxy server status



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli proxy server stop

```
Stop the skysocks server

Usage:
  skywire cli proxy server stop



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli proxy start

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

### skywire cli proxy status

```
proxy client status

Usage:
  skywire cli proxy status



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli proxy stop

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

### skywire cli proxy test

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

## skywire cli pv

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

## skywire cli reward

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

### skywire cli reward rules

```
display the mainnet rules

Usage:
  skywire cli reward rules



Flags:
  -l, --html   render html from markdown
  -r, --raw    print raw the embedded file
```

## skywire cli rewards

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

### skywire cli rewards bot

```
reward notification telegram bot

Usage:
  skywire cli rewards bot



Flags:
  -w, --watch string   File to watch - file where reward transaction IDs are recorded (default "../reward/rewards/transactions0.txt")
```

### skywire cli rewards bw-collect

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

### skywire cli rewards loginchain

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

### skywire cli rewards script

```
Print the reward system scripts. Pipe to bash to execute.

Usage:
  skywire cli rewards script

Available Commands:
  getlogs                 getlogs.sh - `skywire cli log` wrapper
  reward                  reward.sh - `skywire cli rewards` wrapper script
```

#### skywire cli rewards script getlogs

```
getlogs.sh - `skywire cli log` wrapper

Usage:
  skywire cli rewards script getlogs



Flags:
  -m, --minv string   minimum version
```

#### skywire cli rewards script reward

```
reward.sh - `skywire cli rewards` wrapper script

Usage:
  skywire cli rewards script reward



Flags:
  -d, --date string   date for which to calculate rewards
```

### skywire cli rewards svc

```
verify services in survey

Usage:
  skywire cli rewards svc



Flags:
  -s, --loglvl string   [ debug | warn | error | fatal | panic | trace ] (default "info")
  -k, --pk string       verify services in survey for pubkey
  -p, --lpath string    path to the surveys (default "log_collecting")
```

### skywire cli rewards systemd

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

### skywire cli rewards tp-collect

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

### skywire cli rewards ui

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

## skywire cli rg

```
View active route groups, their associated apps, and live traffic stats.

Usage:
  skywire cli rg

Available Commands:
  ls                      List active route groups with app associations and live stats

Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli rg ls

```
List active route groups with app associations and live stats

Usage:
  skywire cli rg ls



Flags:
      --json   output as JSON

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

## skywire cli route

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

### skywire cli route add

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

#### skywire cli route add a

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

#### skywire cli route add b

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

#### skywire cli route add c

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

### skywire cli route calc

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

### skywire cli route find

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

### skywire cli route groups

```

    List active route groups with their consume and forward rules

Usage:
  skywire cli route groups



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli route rm

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

### skywire cli route rsn-stats

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

## skywire cli sd

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

## skywire cli skychat

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

### skywire cli skychat listen

```
Connect to skychat SSE endpoint and display incoming messages.

Usage:
  skywire cli skychat listen



Flags:
  -n, --net string   filter by network type (optional)

Global Flags:
      --addr string   skychat HTTP address (default "127.0.0.1:8001")
```

### skywire cli skychat send

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

## skywire cli skynet

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

### skywire cli skynet curl

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

### skywire cli skynet port

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

#### skywire cli skynet port add

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

#### skywire cli skynet port ls

```
List forwarded ports

Usage:
  skywire cli skynet port ls



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli skynet port rm

```
Remove a forwarded port

Usage:
  skywire cli skynet port rm <port>



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli skynet srv

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

#### skywire cli skynet srv start

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

#### skywire cli skynet srv status

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

#### skywire cli skynet srv stop

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

### skywire cli skynet start

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

### skywire cli skynet status

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

### skywire cli skynet stop

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

## skywire cli survey

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

## skywire cli svc

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

### skywire cli svc ar

```

    Query Address Resolver service endpoints

Usage:
  skywire cli svc ar



Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli svc dmsgd

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

#### skywire cli svc dmsgd all-servers

```
List all DMSG servers

Usage:
  skywire cli svc dmsgd all-servers



Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli svc dmsgd clients

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

#### skywire cli svc dmsgd server-clients

```
List all clients grouped by server

Usage:
  skywire cli svc dmsgd server-clients



Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli svc health

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

### skywire cli svc nm

```

    Query Network Monitor service status

Usage:
  skywire cli svc nm



Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli svc tpd

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

#### skywire cli svc tpd bandwidth

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

#### skywire cli svc tpd bandwidth-tp

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

#### skywire cli svc tpd metrics-tp

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

#### skywire cli svc tpd metrics-visor

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

#### skywire cli svc tpd per-key-stats

```
Per-visor transport statistics

Usage:
  skywire cli svc tpd per-key-stats



Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli svc tpd stats

```
Network-wide transport statistics

Usage:
  skywire cli svc tpd stats



Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli svc tpd versions

```
Version statistics from transport discovery

Usage:
  skywire cli svc tpd versions



Global Flags:
      --direct       query directly instead of via visor RPC
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli svc tpd versions-pk

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

#### skywire cli svc tpd visor-stats

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

## skywire cli tp

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

### skywire cli tp add

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

#### skywire cli tp add edge

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

#### skywire cli tp add pv

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

### skywire cli tp auto

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

### skywire cli tp disc

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

### skywire cli tp id

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

### skywire cli tp metrics

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

### skywire cli tp net-stats

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

### skywire cli tp rm

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

### skywire cli tp sync

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

### skywire cli tp tpd-health

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

### skywire cli tp tpd-stats

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

### skywire cli tp tree

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

### skywire cli tp uptime

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

### skywire cli tp v

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

### skywire cli tp viz

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

## skywire cli tps

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

### skywire cli tps add

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

### skywire cli tps list

```
Get the list of transports from a target visor.

Usage:
  skywire cli tps list



Flags:
  -t, --target string   target visor public key

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli tps rm

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

## skywire cli ut

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

### skywire cli ut mdisc

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

#### skywire cli ut mdisc graph

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

### skywire cli ut sd

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

#### skywire cli ut sd graph

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

### skywire cli ut tpd

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

#### skywire cli ut tpd graph

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

## skywire cli util

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

### skywire cli util edit

```
Embedded terminal text editor with syntax highlighting (Ctrl+S save, Ctrl+Q quit)

Usage:
  skywire cli util edit [file]


```

### skywire cli util got

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

#### skywire cli util got dl

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

#### skywire cli util got head

```
Show response headers (HEAD request)

Usage:
  skywire cli util got head <URL>



Flags:
  -A, --agent string     user agent string
  -H, --header strings   HTTP header "Key: Value"
  -x, --proxy string     SOCKS5 proxy address (host:port)
```

#### skywire cli util got req

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

### skywire cli util jq

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

### skywire cli util serve

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

## skywire cli visor

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

### skywire cli visor app

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

#### skywire cli visor app arg

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

##### skywire cli visor app arg autostart

```
Set app autostart

Usage:
  skywire cli visor app arg autostart <name> (true|false)



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor app arg killswitch

```

  Set app killswitch

Usage:
  skywire cli visor app arg killswitch <name> (true|false)



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor app arg netifc

```
Set app network interface.

  "remove" is a special arg to remove the netifc

Usage:
  skywire cli visor app arg netifc <name> <interface>



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor app arg passcode

```

  Set app passcode.

  "remove" is a special arg to remove the passcode

Usage:
  skywire cli visor app arg passcode <name> <passcode>



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

##### skywire cli visor app arg secure

```

  Set app secure

Usage:
  skywire cli visor app arg secure <name> (true|false)



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor app deregister

```

  Deregister app

Usage:
  skywire cli visor app deregister



Flags:
  -k, --procKey string   proc key of the app to deregister

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor app log

```

  Logs from app since RFC3339Nano-formatted timestamp.

  "beginning" is a special timestamp to fetch all the logs

Usage:
  skywire cli visor app log <name> <timestamp>



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor app ls

```

  List apps

Usage:
  skywire cli visor app ls



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor app register

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

#### skywire cli visor app start

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

#### skywire cli visor app stop

```

  Halt app

Usage:
  skywire cli visor app stop <name>



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli visor dmsg-servers

```

  List of connected DMSG servers sorted by latency (lowest first)

Usage:
  skywire cli visor dmsg-servers



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli visor go

```

  Returns Go runtime statistics including goroutine count and memory usage

Usage:
  skywire cli visor go



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli visor halt

```

  Stop a running visor

Usage:
  skywire cli visor halt



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli visor hv

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

#### skywire cli visor hv cpk

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

#### skywire cli visor hv disable

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

#### skywire cli visor hv enable

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

#### skywire cli visor hv pk

```
Public key of remote hypervisor(s) which are currently connected to

Usage:
  skywire cli visor hv pk



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor hv status

```
Check if hypervisor is enabled

Usage:
  skywire cli visor hv status



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor hv ui

```

  open Hypervisor UI in default browser

Usage:
  skywire cli visor hv ui



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli visor info

```

  Summary of visor info

Usage:
  skywire cli visor info



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli visor ip

```

  IP information of network

Usage:
  skywire cli visor ip



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli visor log

```

  Returns runtime logs from the visor

Usage:
  skywire cli visor log



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli visor ping

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

#### skywire cli visor ping bandwidth

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

#### skywire cli visor ping graph

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

#### skywire cli visor ping stop-all

```
Stop all active ping connections and clean up their routes.

Use this to clean up orphaned routes from interrupted ping operations.

Usage:
  skywire cli visor ping stop-all



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli visor ping test

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

#### skywire cli visor ping tree

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

#### skywire cli visor ping tree2

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

### skywire cli visor pk

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

### skywire cli visor ports

```

  List of all ports used by visor services and apps

Usage:
  skywire cli visor ports



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli visor proxies

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

#### skywire cli visor proxies set

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

#### skywire cli visor proxies upstream

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

### skywire cli visor ready

```

  Polls the visor and exits once startup is complete.
  Useful in scripts and systemd ExecStartPost.

Usage:
  skywire cli visor ready



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli visor reinit

```

  Reinitiate modules

Usage:
  skywire cli visor reinit



Flags:
  -m, --module string   target module for reinitiating.

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli visor reward

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

### skywire cli visor start

```
start visor

Usage:
  skywire cli visor start



Flags:
  -s, --src   'go run' external commands from the skywire sources

Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli visor user

```
Show the user the visor process is running as

Usage:
  skywire cli visor user



Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli visor ver

```

  Version and build info

Usage:
  skywire cli visor ver



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

## skywire cli vpn

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

### skywire cli vpn list

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

### skywire cli vpn server

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

#### skywire cli vpn server start

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

#### skywire cli vpn server status

```
Show VPN server status

Usage:
  skywire cli vpn server status



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

#### skywire cli vpn server stop

```
Stop the VPN server

Usage:
  skywire cli vpn server stop



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli vpn start

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

### skywire cli vpn status

```
vpn client status

Usage:
  skywire cli vpn status



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli vpn stop

```
stop the vpnclient

Usage:
  skywire cli vpn stop



Global Flags:
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

### skywire cli vpn ui

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

### skywire cli vpn url

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

