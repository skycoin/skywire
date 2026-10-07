# skywire svc ar

[← skywire svc](../README.md)

```
┌─┐┌┬┐┌┬┐┬─┐┌─┐┌─┐┌─┐   ┬─┐┌─┐┌─┐┌─┐┬  ┬  ┬┌─┐┬─┐
├─┤ ││ ││├┬┘├┤ └─┐└─┐───├┬┘├┤ └─┐│ ││  └┐┌┘├┤ ├┬┘
┴ ┴─┴┘─┴┘┴└─└─┘└─┘└─┘   ┴└─└─┘└─┘└─┘┴─┘ └┘ └─┘┴└─
Address Resolver Server - resolves visor addresses for STCPR/SUDPH connections.

Depends: redis

Production: dmsg://03234b2ee4128d1f78c180d06911102906c80795dfe41bd6253f2619c8b6252a02:80
            dmsg://03234b2ee4128d1f78c180d06911102906c80795dfe41bd6253f2619c8b6252a02:80
Test:       dmsg://03234b2ee4128d1f78c180d06911102906c80795dfe41bd6253f2619c8b6252a02:80
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
  {
      "build_info": {
        "version": "v1.3.29"
      },
      "dmsg_address": "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:80",
      "dmsg_servers": [
        "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"
      ],
      "started_at": "2024-01-15T10:00:00Z"
    }

POST /bind/stcpr (auth)
  Request:  {
      "port": 30178
    }
  Response: 200 OK

DEL /bind/stcpr (auth)
  Response: 200 OK

GET /resolve/stcpr/{pk}
  {
      "addr": "192.168.1.100:30178"
    }

GET /resolve/sudph/{pk}
  {
      "addr": "192.168.1.100:30178",
      "handshake": "\u003cbase64_handshake_data\u003e"
    }

GET /transports
  {
      "sudph": [
        "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"
      ],
      "stcpr": [
        "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5",
        "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"
      ]
    }

DEL /deregister/{network} (NM auth headers: NM-PK, NM-Sign)
  Request:  [
      "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5",
      "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"
    ]
  Response: 200 OK

GET /security/nonces/{pk}
  {
      "nonce": 12345
    }

Note: the specified UDP port must be accessible from the internet for SUDPH.

Example:
  skywire cli config gen-keys > ar-config.json
  skywire svc ar --addr ":9093" --redis "redis://localhost:6379" --sk $(tail -n1 ar-config.json)
```

## Usage

```
skywire svc ar
```

## Flags

```
  -a, --addr string                 plain-HTTP listen address (default ":9093")
      --charts-addr string          serve only the charts page over plain HTTP on this address
  -c, --config string               path to a JSON config file; keys it sets override these flags
                                    (generate one with: skywire cli config gen --ar)
      --dmsg-disc string            url of dmsg-discovery (default "dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80")
      --dmsg-port uint16            dmsghttp listener port (default 80)
      --dmsg-server-type string     type of dmsg server on dmsghttp handler
      --entry-timeout duration      how long an entry lives without a refresh (default 5m0s)
      --keyfile string              file holding the secret key (generated if missing)
  -l, --loglvl string               log level [trace|debug|info|warn|error|fatal|panic] (default "info")
  -m, --metrics string              address to serve Prometheus metrics on
      --mode string                 listeners: http|dmsg|dual (default dual with a key, else http; env SKYWIRE_SVC_MODE overrides)
  -r, --pprofaddr string            address http profiling serves on; alone it implies --pprofmode http (default localhost:6060)
  -q, --pprofmode string            [ cpu | mem | mutex | block | trace | http ]
      --public-udp-address string   externally-reachable host:port advertised in /health for SUDPH
                                    required for visors that reach this AR over dmsghttp
      --redis string                redis URL of the store (default redis://localhost:6379; with --testing and none, the store is in memory)
      --redis-pool-size int         redis connection pool size (default 10)
      --sk cipher.SecKey            dmsg secret key (default 0000000000000000000000000000000000000000000000000000000000000000)
      --tag string                  logging tag (default "address_resolver")
      --test-environment            use the test deployment's defaults instead of production's
  -t, --testing                     run for a test network: keep entries in memory unless --redis is set
      --udp-addr string             UDP address to bind to for SUDPH (default ":30178")
      --whitelist-keys string       network-monitor keys allowed to deregister entries, comma-separated
```

## Global Flags

```
  -h, --help        show help menu
      --jq string   filter JSON output through a jq/gojq expression (implies --json)
      --json        print output as JSON
      --shape       print the output schema skeleton (zero values, all fields) instead of data
      --tui         browse commands and help interactively
```

---
_Generated by `skywire doc` — do not edit by hand._
