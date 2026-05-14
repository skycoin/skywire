# skywire svc tpd

[← skywire svc](../README.md)

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

GET /all-transports?selfTransports=hide
  [
      {
        "entry": {
          "edges": [
            "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5",
            "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"
          ],
          "t_id": "e7a7f1b3c04047f89e12a0a1459b3456",
          "type": "stcpr"
        },
        "latency_ms": 45.2,
        "registered": 1705312800,
        "signatures": [
          "00000000...00000000",
          "00000000...00000000"
        ]
      }
    ]

GET /all-transports/stats
  {
      "by_type": {
        "stcpr": 100,
        "sudph": 50
      },
      "total_transports": 150,
      "unique_visors": 75
    }

GET /all-transports/per-key-stats
  {
      "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5": {
        "stcpr": 3,
        "sudph": 2,
        "total": 5
      }
    }

GET /transports/id:{id} (auth)
  {
      "entry": {
        "edges": [
          "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5",
          "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"
        ],
        "t_id": "e7a7f1b3c04047f89e12a0a1459b3456",
        "type": "stcpr"
      },
      "registered": 1705312800,
      "signatures": [
        "00000000...00000000",
        "00000000...00000000"
      ]
    }

GET /transports/edge:{pk} (auth)
  [<signed_entry>, ...]

GET /transports/stats/{edge}
  {
      "by_type": {
        "stcpr": 3,
        "sudph": 2
      },
      "total": 5
    }

POST /transports/ (auth)
  Request:  [
      {
        "entry": {
          "edges": [
            "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5",
            "03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"
          ],
          "t_id": "e7a7f1b3c04047f89e12a0a1459b3456",
          "type": "stcpr"
        },
        "signatures": [
          "00000000...00000000",
          "00000000...00000000"
        ]
      }
    ]
  Response: <same with registered timestamp>

DEL /transports/id:{id} (auth)
  Response: "transport deleted"

DEL /transports/deregister (NM auth headers: NM-PK, NM-Sign)
  Request:  [
      "e7a7f1b3c04047f89e12a0a1459b3456"
    ]
  Response: 200 OK

GET /bandwidth/transport/{id}?period=daily&limit=7
  [
      {
        "sent_bytes": 1073741824,
        "recv_bytes": 2147483648
      }
    ]

GET /bandwidth/visor/{pk}?period=daily&limit=7
  {
      "sent_bytes": 5368709120,
      "recv_bytes": 10737418240
    }

GET /uptimes
  [
      {
        "on": true,
        "pk": "02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5",
        "tp_count": 5
      }
    ]

GET /security/nonces/{pk}
  {
      "nonce": 12345
    }

Example:
  skywire cli config gen-keys | tee tpd-keys.txt
  transport-discovery --sk $(tail -n1 tpd-keys.txt)
```

## Usage

```
skywire svc tpd
```

## Flags

```
  -a, --addr string                                                                    address to bind to
                                                                                        (default ":9091")
  -c, --config skywire cli config gen --tpd -o /etc/skywire/transport-discovery.json   path to JSON config file. When set, fields below come from the config file. Generate one with skywire cli config gen --tpd -o /etc/skywire/transport-discovery.json.
                                                                                       
      --dmsg-disc string                                                               url of dmsg-discovery
                                                                                        (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string                                                        type of dmsg server on dmsghttp handler
      --dmsgPort uint16                                                                dmsg port value
                                                                                        (default 80)
      --entry-timeout duration                                                         transport entry TTL (0 to disable)
                                                                                        (default 5m0s)
      --keyfile string                                                                 path to file containing secret key (auto-generated if missing)
                                                                                       
  -l, --loglvl string                                                                  [info|error|warn|debug|trace|panic]
                                                                                        (default "info")
  -m, --metrics string                                                                 address to bind metrics API to
      --mode string                                                                    listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string                                                                   address to bind pprof debug server (e.g. localhost:6060)
      --redis string                                                                   connections string for a redis store
                                                                                        (default "redis://localhost:6379")
      --redis-pool-size int                                                            redis connection pool size
                                                                                        (default 10)
      --sk cipher.SecKey                                                               dmsg secret key
                                                                                        (default 0000000000000000000000000000000000000000000000000000000000000000)
      --store-data-path string                                                         path for bandwidth backup files
                                                                                        (default "/var/lib/skywire/tpd/bandwidth")
      --tag string                                                                     logging tag
                                                                                        (default "transport_discovery")
      --test-environment                                                               distinguished between prod and test environment
  -t, --testing                                                                        enable testing to start without redis
      --uptime-db string                                                               path for the service-self uptime bbolt store (empty disables) (default "/var/lib/skywire/tpd/uptime.db")
      --whitelist-keys string                                                          list of whitelisted keys of network monitor used for deregistration
```

---
_Generated by `skywire doc` — do not edit by hand._
