# skywire dmsg disc

[← skywire dmsg](../README.md)

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
{
      "build_info": {
        "commit": "<commit>",
        "date": "<build-date>",
        "version": "<version>"
      },
      "dmsg_address": "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb:80",
      "dmsg_servers": [
        "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb",
        "02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"
      ],
      "started_at": "2024-01-15T10:00:00Z"
    }

GET /dmsg-discovery/entry/{pk} (client entry)
{
      "client": {
        "delegated_servers": [
          "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb",
          "02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"
        ]
      },
      "sequence": 1,
      "static": "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5",
      "timestamp": 1705315200,
      "version": "1.0"
    }

GET /dmsg-discovery/entry/{pk} (server entry)
{
      "version": "",
      "sequence": 0,
      "timestamp": 0,
      "static": "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb",
      "server": {
        "address": "139.162.160.227:30086",
        "availableSessions": 0
      }
    }

POST /dmsg-discovery/entry/ (new entry)
{
      "code": 200,
      "message": "wrote a new entry"
    }

POST /dmsg-discovery/entry/ (update entry)
{
      "code": 200,
      "message": "wrote new entry iteration"
    }

DEL /dmsg-discovery/entry
{
      "code": 200,
      "message": "deleted entry"
    }

GET /dmsg-discovery/entries (all client and server entries)
[
      {
        "client": {
          "delegated_servers": [
            "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb",
            "02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"
          ]
        },
        "sequence": 1,
        "static": "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5",
        "timestamp": 1705315200,
        "version": "1.0"
      },
      {
        "version": "",
        "sequence": 0,
        "timestamp": 0,
        "static": "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb",
        "server": {
          "address": "139.162.160.227:30086",
          "availableSessions": 0
        }
      },
      {
        "version": "",
        "sequence": 0,
        "timestamp": 0,
        "static": "02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd",
        "server": {
          "address": "139.162.173.101:30082",
          "availableSessions": 0
        }
      }
    ]

GET /dmsg-discovery/visorEntries (client entries only)
[
      {
        "client": {
          "delegated_servers": [
            "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb",
            "02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"
          ]
        },
        "sequence": 1,
        "static": "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5",
        "timestamp": 1705315200,
        "version": "1.0"
      }
    ]

GET /dmsg-discovery/available_servers (servers with available_streams > 0)
[
      {
        "version": "",
        "sequence": 0,
        "timestamp": 0,
        "static": "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb",
        "server": {
          "address": "139.162.160.227:30086",
          "availableSessions": 0
        }
      },
      {
        "version": "",
        "sequence": 0,
        "timestamp": 0,
        "static": "02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd",
        "server": {
          "address": "139.162.173.101:30082",
          "availableSessions": 0
        }
      }
    ]

GET /dmsg-discovery/all_servers (all server entries)
[
      {
        "version": "",
        "sequence": 0,
        "timestamp": 0,
        "static": "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb",
        "server": {
          "address": "139.162.160.227:30086",
          "availableSessions": 0
        }
      },
      {
        "version": "",
        "sequence": 0,
        "timestamp": 0,
        "static": "02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd",
        "server": {
          "address": "139.162.173.101:30082",
          "availableSessions": 0
        }
      }
    ]

GET /dmsg-discovery/servers/clients
{
      "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb": [
        "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5",
        "024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7"
      ]
    }

GET /dmsg-discovery/server/{pk}/clients
[
      "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5",
      "024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7"
    ]

Example:
  skywire cli config gen-keys > dmsgd-config.json
  skywire dmsg disc --sk $(tail -n1 dmsgd-config.json)
```

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
  -h, --help        show help menu
      --with-kill   force exit after 3 interrupt signals (default true)
```

---
_Generated by `skywire doc` — do not edit by hand._
