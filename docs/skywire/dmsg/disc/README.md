# skywire dmsg disc

[← skywire dmsg](../README.md)

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
        [1m[94m"commit"[0m[1m:[0m [32m"abc1234"[0m[1m,[0m
        [1m[94m"date"[0m[1m:[0m [32m"<build-date>"[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"<version>"[0m
      [1m}[0m[1m,[0m
      [1m[94m"dmsg_address"[0m[1m:[0m [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb:80"[0m[1m,[0m
      [1m[94m"dmsg_servers"[0m[1m:[0m [1m[[0m
        [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m,
        [32m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"[0m
      [1m][0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"2024-01-15T10:00:00Z"[0m
    [1m}[0m

GET /dmsg-discovery/entry/{pk} (client entry)
[1m{[0m
      [1m[94m"client"[0m[1m:[0m [1m{[0m
        [1m[94m"delegated_servers"[0m[1m:[0m [1m[[0m
          [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m,
          [32m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"[0m
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
      [1m[94m"static"[0m[1m:[0m [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m[1m,[0m
      [1m[94m"server"[0m[1m:[0m [1m{[0m
        [1m[94m"address"[0m[1m:[0m [32m"139.162.160.227:30086"[0m[1m,[0m
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
            [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m,
            [32m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"[0m
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
        [1m[94m"static"[0m[1m:[0m [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"139.162.160.227:30086"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
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
      [1m}[0m
    [1m][0m

GET /dmsg-discovery/visorEntries (client entries only)
[1m[[0m
      [1m{[0m
        [1m[94m"client"[0m[1m:[0m [1m{[0m
          [1m[94m"delegated_servers"[0m[1m:[0m [1m[[0m
            [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m,
            [32m"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"[0m
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
        [1m[94m"static"[0m[1m:[0m [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"139.162.160.227:30086"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
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
      [1m}[0m
    [1m][0m

GET /dmsg-discovery/all_servers (all server entries)
[1m[[0m
      [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"sequence"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"timestamp"[0m[1m:[0m [33m0[0m[1m,[0m
        [1m[94m"static"[0m[1m:[0m [32m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m[1m,[0m
        [1m[94m"server"[0m[1m:[0m [1m{[0m
          [1m[94m"address"[0m[1m:[0m [32m"139.162.160.227:30086"[0m[1m,[0m
          [1m[94m"availableSessions"[0m[1m:[0m [33m0[0m
        [1m}[0m
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
      [1m}[0m
    [1m][0m

GET /dmsg-discovery/servers/clients
[1m{[0m
      [1m[94m"0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"[0m[1m:[0m [1m[[0m
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

## Usage

```
skywire dmsg disc
```

## Flags

```
  -a, --addr string                                                                    address to bind to
                                                                                        (default ":9090")
      --auth string                                                                    auth passphrase as simple auth for official dmsg servers registration
  -c, --config skywire cli config gen --dmsgdisc -o /etc/skywire/dmsg-discovery.json   path to JSON config file. When set, every other CLI flag below is ignored — fields come from the config file. Generate one with skywire cli config gen --dmsgdisc -o /etc/skywire/dmsg-discovery.json.
                                                                                       
      --dmsg-server-type string                                                        type of dmsg server on dmsghttp handler
      --dmsgPort uint16                                                                dmsg port value
                                                                                        (default 80)
      --enable-load-testing                                                            enable load testing
      --entry-timeout duration                                                         client discovery entry TTL (0 to disable)
                                                                                        (default 1h0m0s)
      --keyfile string                                                                 path to file containing secret key (auto-generated if missing)
                                                                                       
  -m, --metrics string                                                                 address to serve metrics API from
      --mode string                                                                    listener mode: http|dual (dmsg-only is rejected — dmsg-servers reach this service over HTTP)
      --official-servers string                                                        list of official dmsg servers keys separated by comma
      --pprofaddr string                                                               pprof http port (default "localhost:6060")
      --pprofmode string                                                               [ cpu | mem | mutex | block | trace | http ]
      --redis string                                                                   connections string for a redis store
                                                                                        (default "redis://localhost:6379")
      --sk cipher.SecKey                                                               dmsg secret key
                                                                                        (default 0000000000000000000000000000000000000000000000000000000000000000)
      --syslog string                                                                  address in which to dial to syslog server
      --syslog-lvl string                                                              minimum log level to report (default "debug")
      --syslog-net string                                                              network in which to dial to syslog server (default "udp")
      --tag string                                                                     tag used for logging and metrics (default "dmsg_disc")
      --test-environment                                                               distinguished between prod and test environment
  -t, --test-mode                                                                      in testing mode
      --whitelist-keys string                                                          list of whitelisted keys of network monitor used for deregistration
```

## Global Flags

```
      --with-kill   force exit after 3 interrupt signals (default true)
```

---
_Generated by `skywire doc` — do not edit by hand._
