# skywire svc ar

[← skywire svc](../README.md)

┌─┐┌┬┐┌┬┐┬─┐┌─┐┌─┐┌─┐   ┬─┐┌─┐┌─┐┌─┐┬  ┬  ┬┌─┐┬─┐
├─┤ ││ ││├┬┘├┤ └─┐└─┐───├┬┘├┤ └─┐│ ││  └┐┌┘├┤ ├┬┘
┴ ┴─┴┘─┴┘┴└─└─┘└─┘└─┘   ┴└─└─┘└─┘└─┘┴─┘ └┘ └─┘┴└─
Address Resolver Server - resolves visor addresses for STCPR/SUDPH connections.

Depends: redis

Production: http://ar.skywire.skycoin.com
            dmsg://03234b2ee4128d1f78c180d06911102906c80795dfe41bd6253f2619c8b6252a02:80
Test:       http://ar.skywire.dev
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
  [1m{[0m
      [1m[94m"build_info"[0m[1m:[0m [1m{[0m
        [1m[94m"version"[0m[1m:[0m [32m"<version>"[0m
      [1m}[0m[1m,[0m
      [1m[94m"dmsg_address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:80"[0m[1m,[0m
      [1m[94m"dmsg_servers"[0m[1m:[0m [1m[[0m
        [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
      [1m][0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"<build-date>"[0m
    [1m}[0m

POST /bind/stcpr (auth)
  Request:  [1m{[0m
      [1m[94m"port"[0m[1m:[0m [33m30178[0m
    [1m}[0m
  Response: 200 OK

DEL /bind/stcpr (auth)
  Response: 200 OK

GET /resolve/stcpr/{pk}
  [1m{[0m
      [1m[94m"addr"[0m[1m:[0m [32m"192.168.1.100:30178"[0m
    [1m}[0m

GET /resolve/sudph/{pk}
  [1m{[0m
      [1m[94m"addr"[0m[1m:[0m [32m"192.168.1.100:30178"[0m[1m,[0m
      [1m[94m"handshake"[0m[1m:[0m [32m"[0m[35m\u003c[0m[32mbase64_handshake_data[0m[35m\u003e[0m[32m"[0m
    [1m}[0m

GET /transports
  [1m{[0m
      [1m[94m"sudph"[0m[1m:[0m [1m[[0m
        [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m
      [1m][0m[1m,[0m
      [1m[94m"stcpr"[0m[1m:[0m [1m[[0m
        [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
        [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
      [1m][0m
    [1m}[0m

DEL /deregister/{network} (NM auth headers: NM-PK, NM-Sign)
  Request:  [1m[[0m
      [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
      [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
    [1m][0m
  Response: 200 OK

GET /security/nonces/{pk}
  [1m{[0m
      [1m[94m"nonce"[0m[1m:[0m [33m12345[0m
    [1m}[0m

Note: the specified UDP port must be accessible from the internet for SUDPH.

Example:
  skywire cli config gen-keys > ar-config.json
  skywire svc ar --addr ":9093" --redis "redis://localhost:6379" --sk $(tail -n1 ar-config.json)

## Usage

```
skywire svc ar
```

## Flags

```
  -a, --addr string                                                                address to bind to
                                                                                    (default ":9093")
  -c, --config skywire cli config gen --ar -o /etc/skywire/address-resolver.json   path to JSON config file. Generate with skywire cli config gen --ar -o /etc/skywire/address-resolver.json.
                                                                                   
      --dmsg-disc string                                                           url of dmsg discovery
                                                                                    (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string                                                    type of dmsg server on dmsghttp handler
      --dmsgPort uint16                                                            dmsg port value
                                                                                    (default 80)
      --entry-timeout duration                                                     address binding TTL (0 to disable)
                                                                                    (default 5m0s)
      --keyfile string                                                             path to file containing secret key (auto-generated if missing)
                                                                                   
  -l, --loglvl string                                                              [info|error|warn|debug|trace|panic]
                                                                                    (default "info")
  -m, --metrics string                                                             address to bind metrics API to
      --mode string                                                                listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string                                                               address to bind pprof debug server (e.g. localhost:6060)
      --public-udp-address string                                                  externally-reachable host:port advertised in /health for SUDPH
                                                                                   required for visors that reach this AR over dmsghttp
      --redis string                                                               connections string for a redis store
                                                                                    (default "redis://localhost:6379")
      --redis-pool-size int                                                        redis connection pool size
                                                                                    (default 10)
      --sk cipher.SecKey                                                           dmsg secret key
                                                                                    (default 0000000000000000000000000000000000000000000000000000000000000000)
      --tag string                                                                 logging tag
                                                                                    (default "address_resolver")
      --test-environment                                                           distinguished between prod and test environment
  -t, --testing                                                                    enable testing to start without redis
      --udp-addr string                                                            UDP address to bind to for SUDPH
                                                                                    (default ":30178")
      --whitelist-keys string                                                      list of whitelisted keys of network monitor used for deregistration
```

---
_Generated by `skywire doc` — do not edit by hand._
