# skywire svc sd

[← skywire svc](../README.md)

┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐   ┌┬┐┬┌─┐┌─┐┌─┐┬  ┬┌─┐┬─┐┬ ┬
└─┐├┤ ├┬┘└┐┌┘││  ├┤ ─── │││└─┐│  │ │└┐┌┘├┤ ├┬┘└┬┘
└─┘└─┘┴└─ └┘ ┴└─┘└─┘   ─┴┘┴└─┘└─┘└─┘ └┘ └─┘┴└─ ┴ 
Service Discovery Server - registers and discovers services (VPN, proxy, visor).

Depends: redis

Production: http://sd.skycoin.com
            dmsg://0204890f9def4f9a5448c2e824c6a4afc85fd1f877322320898fafdf407cc6fef7:80
Test:       http://sd.skywire.dev
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

GET /api/services?type=vpn&version=v1.3&country=US&quantity=10
  [1m[[0m
      [1m{[0m
        [1m[94m"address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:3"[0m[1m,[0m
        [1m[94m"geo"[0m[1m:[0m [1m{[0m
          [1m[94m"country"[0m[1m:[0m [32m"US"[0m[1m,[0m
          [1m[94m"lat"[0m[1m:[0m [33m37.77[0m[1m,[0m
          [1m[94m"lon"[0m[1m:[0m [33m-122.41[0m[1m,[0m
          [1m[94m"region"[0m[1m:[0m [32m"CA"[0m
        [1m}[0m[1m,[0m
        [1m[94m"type"[0m[1m:[0m [32m"vpn"[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m
    [1m][0m

GET /api/services/{addr}?type=vpn
  [1m{[0m
      [1m[94m"address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:3"[0m[1m,[0m
      [1m[94m"geo"[0m[1m:[0m [1m{[0m
        [1m[94m"country"[0m[1m:[0m [32m"US"[0m[1m,[0m
        [1m[94m"lat"[0m[1m:[0m [33m37.77[0m[1m,[0m
        [1m[94m"lon"[0m[1m:[0m [33m-122.41[0m[1m,[0m
        [1m[94m"region"[0m[1m:[0m [32m"CA"[0m
      [1m}[0m[1m,[0m
      [1m[94m"type"[0m[1m:[0m [32m"vpn"[0m[1m,[0m
      [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
    [1m}[0m

POST /api/services (auth)
  Request:  [1m{[0m
      [1m[94m"address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:3"[0m[1m,[0m
      [1m[94m"type"[0m[1m:[0m [32m"vpn"[0m[1m,[0m
      [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
    [1m}[0m
  Response: (same with geo data added)

DEL /api/services/{addr}?type=vpn (auth)
  Response: true

DEL /api/services/deregister/{type} (NM auth headers: NM-PK, NM-Sign)
  Request:  [1m[[0m
      [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
      [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
    [1m][0m
  Response: true

GET /security/nonces/{pk}
  [1m{[0m
      [1m[94m"nonce"[0m[1m:[0m [33m12345[0m
    [1m}[0m

Example:
  skywire cli config gen-keys | tee sd-keys.txt
  service-discovery --sk $(tail -n1 sd-keys.txt)

## Usage

```
skywire svc sd
```

## Flags

```
  -a, --addr string                                                                 address to bind to
                                                                                     (default ":9098")
  -c, --config skywire cli config gen --sd -o /etc/skywire/service-discovery.json   path to JSON config file. Generate with skywire cli config gen --sd -o /etc/skywire/service-discovery.json.
                                                                                    
  -d, --dmsg-disc string                                                            url of dmsg-discovery
                                                                                     (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string                                                     type of dmsg server on dmsghttp handler
      --dmsgPort uint16                                                             dmsg port value
                                                                                     (default 80)
      --entry-timeout duration                                                      client service entry TTL (0 to disable)
                                                                                     (default 5m0s)
      --geoip string                                                                url of geoip service
                                                                                     (default "http://ip.skycoin.com")
      --keyfile string                                                              path to file containing secret key (auto-generated if missing)
                                                                                    
  -m, --metrics string                                                              address to bind metrics API to
      --mode string                                                                 listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string                                                                address to bind pprof debug server (e.g. localhost:6060)
  -r, --redis string                                                                connections string for a redis store
                                                                                     (default "redis://localhost:6379")
  -s, --sk cipher.SecKey                                                            dmsg secret key
                                                                                     (default 0000000000000000000000000000000000000000000000000000000000000000)
  -t, --test                                                                        run in test mode and disable auth
  -w, --whitelist-keys string                                                       list of whitelisted keys of network monitor used for deregistration
```

---
_Generated by `skywire doc` — do not edit by hand._
