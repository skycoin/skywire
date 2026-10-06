# skywire svc sd

[← skywire svc](../README.md)

```
┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐   ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
└─┐├┤ ├┬┘└┐┌┘││  ├┤ ─── │││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
└─┘└─┘┴└─ └┘ ┴└─┘└─┘   ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴ 
Service Discovery Server - registers and discovers services (VPN, proxy, visor).

Depends: redis

Production: dmsg://0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7:80
            dmsg://0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7:80
Test:       dmsg://0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7:80
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

GET /api/services?type=vpn&version=v1.3&country=US&quantity=10
  [
      {
        "address": "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:3",
        "geo": {
          "country": "US",
          "lat": 37.77,
          "lon": -122.41,
          "region": "CA"
        },
        "type": "vpn",
        "version": "v1.3.29"
      }
    ]

GET /api/services/{addr}?type=vpn
  {
      "address": "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:3",
      "geo": {
        "country": "US",
        "lat": 37.77,
        "lon": -122.41,
        "region": "CA"
      },
      "type": "vpn",
      "version": "v1.3.29"
    }

POST /api/services (auth)
  Request:  {
      "address": "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:3",
      "type": "vpn",
      "version": "v1.3.29"
    }
  Response: (same with geo data added)

DEL /api/services/{addr}?type=vpn (auth)
  Response: true

DEL /api/services/deregister/{type} (NM auth headers: NM-PK, NM-Sign)
  Request:  [
      "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5",
      "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"
    ]
  Response: true

GET /security/nonces/{pk}
  {
      "nonce": 12345
    }

Example:
  skywire cli config gen-keys | tee sd-keys.txt
  service-discovery --sk $(tail -n1 sd-keys.txt)
```

## Usage

```
skywire svc sd [flags]
```

## Flags

```
  -a, --addr string               plain-HTTP listen address (default ":9098")
      --charts-addr string        serve only the charts page over plain HTTP on this address
  -c, --config string             path to a JSON config file; keys it sets override these flags
                                  (generate one with: skywire cli config gen --sd)
      --dmsg-disc string          url of dmsg-discovery (default "dmsg://022e607e0914d6e7ccda7587f95790c09e126bbd506cc476a1eda852325aadd1aa:80")
      --dmsg-port uint16          dmsghttp listener port (default 80)
      --dmsg-server-type string   type of dmsg server on dmsghttp handler
      --entry-timeout duration    how long an entry lives without a refresh (default 5m0s)
      --geoip string              url of geoip service (default "https://ip.skycoin.com")
      --keyfile string            file holding the secret key (generated if missing)
  -l, --loglvl string             log level [trace|debug|info|warn|error|fatal|panic] (default "info")
  -m, --metrics string            address to serve Prometheus metrics on
      --mode string               listeners: http|dmsg|dual (default dual with a key, else http; env SKYWIRE_SVC_MODE overrides)
  -r, --pprofaddr string          address http profiling serves on; alone it implies --pprofmode http (default localhost:6060)
  -q, --pprofmode string          [ cpu | mem | mutex | block | trace | http ]
      --redis string              redis URL of the store (default redis://localhost:6379; with --testing and none, the store is in memory)
      --redis-pool-size int       redis connection pool size (default 10)
      --sk cipher.SecKey          dmsg secret key (default 0000000000000000000000000000000000000000000000000000000000000000)
      --tag string                logging tag (default "service_discovery")
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
```

---
_Generated by `skywire doc` — do not edit by hand._
