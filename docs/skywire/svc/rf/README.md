# skywire svc rf

[← skywire svc](../README.md)

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

## Usage

```
skywire svc rf
```

## Flags

```
  -a, --addr string                                                            address to bind to
                                                                                (default ":9092")
  -c, --config skywire cli config gen --rf -o /etc/skywire/route-finder.json   path to JSON config file. Generate with skywire cli config gen --rf -o /etc/skywire/route-finder.json.
                                                                               
  -D, --dmsg-disc string                                                       url of dmsg discovery
                                                                                (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string                                                type of dmsg server on dmsghttp handler
      --dmsgPort uint16                                                        dmsg port value
                                                                                (default 80)
      --keyfile string                                                         path to file containing secret key (auto-generated if missing)
                                                                               
  -l, --loglvl string                                                          [info|error|warn|debug|trace|panic]
                                                                                (default "info")
  -m, --metrics string                                                         address to bind metrics API to
      --mode string                                                            listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string                                                           address to bind pprof debug server (e.g. localhost:6060)
      --redis string                                                           connections string for a redis store
                                                                                (default "redis://localhost:6379")
      --redis-pool-size int                                                    redis connection pool size
                                                                                (default 10)
      --sk cipher.SecKey                                                       dmsg secret key
                                                                                (default 0000000000000000000000000000000000000000000000000000000000000000)
      --tag string                                                             logging tag
                                                                                (default "route_finder")
  -t, --testing                                                                enable testing to start without redis
```

---
_Generated by `skywire doc` — do not edit by hand._
