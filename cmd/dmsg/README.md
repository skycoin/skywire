# DMSG

## Subcommand Tree

```
dmsg
├── conf
│   ├── gen-keys
│   └── verify-keys
├── curl
├── disc
├── http
├── ip
├── pty
│   ├── cli
│   │   ├── whitelist
│   │   ├── whitelist-add
│   │   └── whitelist-remove
│   ├── host
│   │   └── confgen
│   └── ui
├── self-ping
├── server
│   ├── config
│   │   └── gen
│   ├── dial
│   └── start
├── socks
│   ├── client
│   └── server
└── web
    └── srv
```

## Command Reference

# skywire dmsg

# skywire dmsg

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

## skywire dmsg conf

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

### skywire dmsg conf gen-keys

```
Generate a new dmsg keypair

Usage:
  skywire dmsg conf gen-keys



Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

### skywire dmsg conf verify-keys

```
Derives the public key from the given secret key. Use to verify PK/SK pairs in config files.

Usage:
  skywire dmsg conf verify-keys [secret-key]



Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

## skywire dmsg curl

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

## skywire dmsg disc

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
      [1m[94m"dmsg_address"[0m[1m:[0m [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7:80"[0m[1m,[0m
      [1m[94m"dmsg_servers"[0m[1m:[0m [1m[[0m
        [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m,
        [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m
      [1m][0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"2024-01-15T10:00:00Z"[0m
    [1m}[0m

GET /dmsg-discovery/entry/{pk} (client entry)
[1m{[0m
      [1m[94m"client"[0m[1m:[0m [1m{[0m
        [1m[94m"delegated_servers"[0m[1m:[0m [1m[[0m
          [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m,
          [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m
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
      [1m[94m"static"[0m[1m:[0m [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m[1m,[0m
      [1m[94m"server"[0m[1m:[0m [1m{[0m
        [1m[94m"address"[0m[1m:[0m [32m"70.121.13.123:9083"[0m[1m,[0m
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
            [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m,
            [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m
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
        [1m[94m"static"[0m[1m:[0m [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"70.121.13.123:9083"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
      [1m}[0m,
      [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"sequence"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"timestamp"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"static"[0m[1m:[0m [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"139.162.160.227:30086"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
      [1m}[0m
    [1m][0m

GET /dmsg-discovery/visorEntries (client entries only)
[1m[[0m
      [1m{[0m
        [1m[94m"client"[0m[1m:[0m [1m{[0m
          [1m[94m"delegated_servers"[0m[1m:[0m [1m[[0m
            [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m,
            [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m
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
        [1m[94m"static"[0m[1m:[0m [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"70.121.13.123:9083"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
      [1m}[0m,
      [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"sequence"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"timestamp"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"static"[0m[1m:[0m [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"139.162.160.227:30086"[0m[1m,[0m
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
        [1m[94m"static"[0m[1m:[0m [32m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"70.121.13.123:9083"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
      [1m}[0m,
      [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"sequence"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"timestamp"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"static"[0m[1m:[0m [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"139.162.160.227:30086"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
      [1m}[0m
    [1m][0m

GET /dmsg-discovery/servers/clients
[1m{[0m
      [1m[94m"0326978f5a53aff537dbb47fed58b1f123af3b00132d365f1309a14db4168dcff7"[0m[1m:[0m [1m[[0m
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

## skywire dmsg http

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

## skywire dmsg ip

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

## skywire dmsg pty

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

### skywire dmsg pty cli

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

#### skywire dmsg pty cli whitelist

```
lists all whitelisted public keys

Usage:
  skywire dmsg pty cli whitelist



Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg pty cli whitelist-add

```
adds public key(s) to the whitelist

Usage:
  skywire dmsg pty cli whitelist-add <public-key>...



Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg pty cli whitelist-remove

```
removes public key(s) from the whitelist

Usage:
  skywire dmsg pty cli whitelist-remove <public-key>...



Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

### skywire dmsg pty host

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

#### skywire dmsg pty host confgen

```
generates config file

Usage:
  skywire dmsg pty host confgen <config.json>



Flags:
      --unsafe   will unsafely write config if set

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

### skywire dmsg pty ui

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

## skywire dmsg self-ping

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

## skywire dmsg server

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

### skywire dmsg server config

```
Generate a dmsg-server config

Usage:
  skywire dmsg server config

Available Commands:
  gen                     Generate a config file

Global Flags:
      --with-kill   force exit after 3 interrupt signals (default true)
```

#### skywire dmsg server config gen

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

### skywire dmsg server dial

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

### skywire dmsg server start

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

## skywire dmsg socks

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

### skywire dmsg socks client

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

### skywire dmsg socks server

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

## skywire dmsg web

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

### skywire dmsg web srv

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

