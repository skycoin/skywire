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
