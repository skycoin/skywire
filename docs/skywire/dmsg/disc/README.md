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
        "commit": "abc1234",
        "date": "2024-01-15T10:30:00Z",
        "version": "v1.3.29"
      },
      "dmsg_address": "0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7:80",
      "dmsg_servers": [
        "0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7",
        "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"
      ],
      "started_at": "2024-01-15T10:00:00Z"
    }

GET /dmsg-discovery/entry/{pk} (client entry)
{
      "client": {
        "delegated_servers": [
          "0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7",
          "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"
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
      "static": "0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7",
      "server": {
        "address": "172.105.179.5:30085",
        "address_ws": "wss://ajkrc67y2ruh3lgv66weyja7acagb4mxfeivkks3m63w6dtzel24o.theskywirenetwork.net/dmsg",
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
            "0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7",
            "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"
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
        "static": "0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7",
        "server": {
          "address": "172.105.179.5:30085",
          "address_ws": "wss://ajkrc67y2ruh3lgv66weyja7acagb4mxfeivkks3m63w6dtzel24o.theskywirenetwork.net/dmsg",
          "availableSessions": 0
        }
      },
      {
        "version": "",
        "sequence": 0,
        "timestamp": 0,
        "static": "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb",
        "server": {
          "address": "139.162.160.227:30086",
          "address_ws": "wss://aka2cawifaqoqejwrsgqfdhrdmnjqucdw4tldpg3r7hitmttqszmw.theskywirenetwork.net/dmsg",
          "availableSessions": 0
        }
      }
    ]

GET /dmsg-discovery/visorEntries (client entries only)
[
      {
        "client": {
          "delegated_servers": [
            "0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7",
            "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"
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
        "static": "0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7",
        "server": {
          "address": "172.105.179.5:30085",
          "address_ws": "wss://ajkrc67y2ruh3lgv66weyja7acagb4mxfeivkks3m63w6dtzel24o.theskywirenetwork.net/dmsg",
          "availableSessions": 0
        }
      },
      {
        "version": "",
        "sequence": 0,
        "timestamp": 0,
        "static": "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb",
        "server": {
          "address": "139.162.160.227:30086",
          "address_ws": "wss://aka2cawifaqoqejwrsgqfdhrdmnjqucdw4tldpg3r7hitmttqszmw.theskywirenetwork.net/dmsg",
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
        "static": "0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7",
        "server": {
          "address": "172.105.179.5:30085",
          "address_ws": "wss://ajkrc67y2ruh3lgv66weyja7acagb4mxfeivkks3m63w6dtzel24o.theskywirenetwork.net/dmsg",
          "availableSessions": 0
        }
      },
      {
        "version": "",
        "sequence": 0,
        "timestamp": 0,
        "static": "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb",
        "server": {
          "address": "139.162.160.227:30086",
          "address_ws": "wss://aka2cawifaqoqejwrsgqfdhrdmnjqucdw4tldpg3r7hitmttqszmw.theskywirenetwork.net/dmsg",
          "availableSessions": 0
        }
      }
    ]

GET /dmsg-discovery/servers/clients
{
      "0255117bf8d4687dacd5f7ac4c241f008060f1972911552a5b67b76f0e7922f5c7": [
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
  -a, --addr string               plain-HTTP listen address (default ":9090")
      --auth string               auth passphrase as simple auth for official dmsg servers registration
      --charts-addr string        serve only the charts page over plain HTTP on this address
  -c, --config string             path to a JSON config file; keys it sets override these flags
                                  (generate one with: skywire cli config gen --dmsgdisc)
      --dmsg-port uint16          dmsghttp listener port (default 80)
      --dmsg-server-type string   type of dmsg server on dmsghttp handler
      --enable-load-testing       enable load testing
      --entry-timeout duration    how long an entry lives without a refresh (default 1h0m0s)
      --keyfile string            file holding the secret key (generated if missing)
  -l, --loglvl string             log level [trace|debug|info|warn|error|fatal|panic] (default "info")
  -m, --metrics string            address to serve Prometheus metrics on
      --mode string               listeners: http|dmsg|dual (default dual with a key, else http; env SKYWIRE_SVC_MODE overrides)
      --official-servers string   list of official dmsg servers keys separated by comma
  -r, --pprofaddr string          address http profiling serves on; alone it implies --pprofmode http (default localhost:6060)
  -q, --pprofmode string          [ cpu | mem | mutex | block | trace | http ]
      --redis string              redis URL of the store (default redis://localhost:6379; with --testing and none, the store is in memory)
      --redis-pool-size int       redis connection pool size (default 10)
      --sk cipher.SecKey          dmsg secret key (default 0000000000000000000000000000000000000000000000000000000000000000)
      --syslog string             address in which to dial to syslog server
      --syslog-net string         network in which to dial to syslog server (default "udp")
      --tag string                logging tag (default "dmsg_disc")
      --test-environment          use the test deployment's defaults instead of production's
  -t, --testing                   run for a test network: keep entries in memory unless --redis is set
      --whitelist-keys string     network-monitor keys allowed to deregister entries, comma-separated
```

## Global Flags

```
  -h, --help        show help menu
      --jq string   filter JSON output through a jq/gojq expression (implies --json)
      --json        print output as JSON
      --shape       print the output schema skeleton (zero values, all fields) instead of data
      --tui         browse commands and help interactively
      --with-kill   force exit after 3 interrupt signals (default true)
```

---
_Generated by `skywire doc` — do not edit by hand._
