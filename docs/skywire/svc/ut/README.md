# skywire svc ut

[← skywire svc](../README.md)

```
┬ ┬┌─┐┌┬┐┬┌┬┐┌─┐   ┌┬┐┬─┐┌─┐┌─┐┬┌─┌─┐┬─┐
│ │├─┘ │ ││││├┤ ─── │ ├┬┘├─┤│  ├┴┐├┤ ├┬┘
└─┘┴   ┴ ┴┴ ┴└─┘    ┴ ┴└─┴ ┴└─┘┴ ┴└─┘┴└─
Uptime Tracker Server - tracks visor online status and uptime statistics.

Depends: redis, postgres

Production: http://ut.skywire.skycoin.com
            dmsg://022c424caa6239ba7d1d9d8f7dab56cd5ec6ae2ea9ad97bb94ad4b48f62a540d3f:80
Test:       http://ut.skywire.dev
            dmsg://022c424caa6239ba7d1d9d8f7dab56cd5ec6ae2ea9ad97bb94ad4b48f62a540d3f:80

HTTP Endpoints:
  GET  /health                        Health check
  GET  /v4/update                     Visor heartbeat (auth)
  GET  /visors                        All registered visors
  GET  /uptimes                       Visor uptime data (?v=v2 for v2 format)
  GET  /uptime/{pk}                   Uptime for specific visor
  GET  /dashboard                     Dashboard chart data
  GET  /visor-ips                     Visor IP addresses (private API)
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

GET /v4/update (auth)
  Response: 200 OK

GET /visors
  [1m[[0m
      [1m{[0m
        [1m[94m"city"[0m[1m:[0m [32m"New York"[0m[1m,[0m
        [1m[94m"country"[0m[1m:[0m [32m"US"[0m[1m,[0m
        [1m[94m"ip"[0m[1m:[0m [32m"192.168.1.1"[0m[1m,[0m
        [1m[94m"online"[0m[1m:[0m [36mtrue[0m[1m,[0m
        [1m[94m"pk"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m
    [1m][0m

GET /uptimes
  [1m[[0m
      [1m{[0m
        [1m[94m"key"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m,[0m
        [1m[94m"online"[0m[1m:[0m [36mtrue[0m
      [1m}[0m,
      [1m{[0m
        [1m[94m"key"[0m[1m:[0m [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m[1m,[0m
        [1m[94m"online"[0m[1m:[0m [36mfalse[0m
      [1m}[0m
    [1m][0m

GET /uptimes?v=v2
  [1m[[0m
      [1m{[0m
        [1m[94m"pk"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m,[0m
        [1m[94m"on"[0m[1m:[0m [36mtrue[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m[1m,[0m
        [1m[94m"daily"[0m[1m:[0m [1m{[0m
          [1m[94m"2024-01-14"[0m[1m:[0m [32m"100.0"[0m[1m,[0m
          [1m[94m"2024-01-15"[0m[1m:[0m [32m"95.5"[0m
        [1m}[0m
      [1m}[0m
    [1m][0m

GET /uptimes?status=on
  (same as /uptimes, filtered to online visors only)

GET /uptime/{pk}
  [1m{[0m
      [1m[94m"key"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m,[0m
      [1m[94m"online"[0m[1m:[0m [36mtrue[0m
    [1m}[0m

GET /dashboard?length=6
  Response: HTML bar chart of monthly node counts

GET /visor-ips?month=all (private API)
  [1m{[0m
      [1m[94m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m[1m:[0m [32m"192.168.1.1"[0m[1m,[0m
      [1m[94m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m[1m:[0m [32m"10.0.0.1"[0m
    [1m}[0m

GET /security/nonces/{pk}
  [1m{[0m
      [1m[94m"nonce"[0m[1m:[0m [33m12345[0m
    [1m}[0m
```

## Usage

```
skywire svc ut
```

## Flags

```
  -a, --addr string              address to bind to
                                  (default ":9096")
      --dmsg-disc string         url of dmsg discovery
                                  (default "http://dmsgd.skywire.skycoin.com")
      --dmsgPort uint16          dmsg port value
                                  (default 80)
      --enable-load-testing      enable load testing
      --geoip string             url of geoip service
                                  (default "http://ip.skycoin.com")
      --keyfile string           path to file containing secret key (auto-generated if missing)
                                 
  -l, --log                      enable request logging
                                  (default true)
  -m, --metrics string           address to bind metrics API to
                                  (default ":2121")
      --mode string              listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pg-host string           host of postgres
                                  (default "localhost")
      --pg-max-open-conn int     maximum open connection of db
                                  (default 60)
      --pg-port string           port of postgres
                                  (default "5432")
      --pprof string             address to bind pprof debug server (e.g. localhost:6060)
  -p, --private-addr string      private address to bind to
                                  (default ":9086")
      --redis string             connections string for a redis store
                                  (default "redis://localhost:6379")
      --redis-pool-size int      redis connection pool size
                                  (default 10)
      --sk cipher.SecKey         dmsg secret key
                                  (default 0000000000000000000000000000000000000000000000000000000000000000)
      --store-data-cutoff int    number of days data store in db
                                  (default 7)
      --store-data-path string   path of db daily data store
                                  (default "/var/lib/skywire/ut/daily")
      --tag string               logging tag
                                  (default "uptime_tracker")
  -t, --testing                  enable testing to start without redis
```

---
_Generated by `skywire doc` — do not edit by hand._
