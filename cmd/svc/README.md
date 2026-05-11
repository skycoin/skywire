# Skywire Services

## Subcommand Tree

```
svc
├── ar
├── conf
│   ├── dmsghttp
│   └── http
├── confbs
├── ip
├── nm
│   └── deregister
├── rf
├── sd
├── se
│   ├── dmsg
│   ├── setup
│   └── visor
├── sn
│   └── health
├── stun
├── tpd
├── tps
│   ├── add
│   ├── list
│   └── rm
└── ut
```

## Command Reference

# skywire svc

# skywire svc

```
┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐   ┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐┌─┐
└─┐├┴┐└┬┘││││├┬┘├┤ ───└─┐├┤ ├┬┘└┐┌┘││  ├┤ └─┐
└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘   └─┘└─┘┴└─ └┘ ┴└─┘└─┘└─┘
	Skywire services

Usage:
  skywire svc

Available Commands:
  ar                      Address Resolver Server for skywire
  conf                    print services-config.json file
  confbs                  Config Bootstrap Server for skywire
  ip                      GeoIP service for skywire
  nm                      Network monitor for skywire VPN and Visor.
  rf                      Route Finder Server for skywire
  sd                      Service discovery server
  se                      skywire environment generator
  sn                      Route Setup Node for skywire
  stun                    STUN server for skywire
  tpd                     Transport Discovery Server for skywire
  tps                     Transport setup server for skywire
  ut                      Uptime Tracker Server for skywire
```

## skywire svc ar

```
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
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m[1m,[0m
      [1m[94m"dmsg_address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:80"[0m[1m,[0m
      [1m[94m"dmsg_servers"[0m[1m:[0m [1m[[0m
        [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
      [1m][0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"2024-01-15T10:00:00Z"[0m
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

Usage:
  skywire svc ar



Flags:
  -a, --addr string               address to bind to
                                   (default ":9093")
      --dmsg-disc string          url of dmsg discovery
                                   (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string   type of dmsg server on dmsghttp handler
      --dmsgPort uint16           dmsg port value
                                   (default 80)
      --entry-timeout duration    address binding TTL (0 to disable)
                                   (default 5m0s)
      --keyfile string            path to file containing secret key (auto-generated if missing)
                                  
  -l, --loglvl string             [info|error|warn|debug|trace|panic]
                                   (default "info")
  -m, --metrics string            address to bind metrics API to
      --mode string               listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string              address to bind pprof debug server (e.g. localhost:6060)
      --redis string              connections string for a redis store
                                   (default "redis://localhost:6379")
      --redis-pool-size int       redis connection pool size
                                   (default 10)
      --sk cipher.SecKey          dmsg secret key
                                   (default 0000000000000000000000000000000000000000000000000000000000000000)
      --tag string                logging tag
                                   (default "address_resolver")
      --test-environment          distinguished between prod and test environment
  -t, --testing                   enable testing to start without redis
      --udp-addr string           UDP address to bind to for SUDPH
                                   (default ":30178")
      --whitelist-keys string     list of whitelisted keys of network monitor used for deregistration
```

## skywire svc conf

```
print the full services-config.json (HTTP + DMSG endpoints)

Usage:
  skywire svc conf

Available Commands:
  dmsghttp                print DMSG-only deployment config
  http                    print HTTP-only deployment config
```

### skywire svc conf dmsghttp

```
print the DMSG-only subset of services-config.json (http fields stripped)

Usage:
  skywire svc conf dmsghttp


```

### skywire svc conf http

```
print the HTTP-only subset of services-config.json (dmsg fields stripped)

Usage:
  skywire svc conf http


```

## skywire svc confbs

```
┌─┐┌─┐┌┐┌┌─┐┬┌─┐   ┌┐ ┌─┐┌─┐┌┬┐┌─┐┌┬┐┬─┐┌─┐┌─┐┌─┐┌─┐┬─┐
│  │ ││││├┤ ││ ┬───├┴┐│ ││ │ │ └─┐ │ ├┬┘├─┤├─┘├─┘├┤ ├┬┘
└─┘└─┘┘└┘└  ┴└─┘   └─┘└─┘└─┘ ┴ └─┘ ┴ ┴└─┴ ┴┴  ┴  └─┘┴└─
Config Bootstrap Server - provides initial configuration for visors.

Production: http://conf.skywire.skycoin.com
Test:       http://conf.skywire.dev

HTTP Endpoints:
  GET  /health     Health check
  GET  /           Bootstrap configuration (services URLs, keys, etc.)
  GET  /dmsghttp   DMSG HTTP configuration

Response Examples (from actual struct types):

GET /health - api.HealthCheckResponse
[1m{[0m
      [1m[94m"build_info"[0m[1m:[0m [1m{[0m
        [1m[94m"commit"[0m[1m:[0m [32m"abc1234"[0m[1m,[0m
        [1m[94m"date"[0m[1m:[0m [32m"2024-01-15T10:30:00Z"[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m[1m,[0m
      [1m[94m"dmsg_address"[0m[1m:[0m [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5:80"[0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"2024-01-15T10:00:00Z"[0m
    [1m}[0m

GET / - visorconfig.Services
[1m{[0m
      [1m[94m"address_resolver"[0m[1m:[0m [32m"http://ar.skywire.skycoin.com"[0m[1m,[0m
      [1m[94m"dmsg_discovery"[0m[1m:[0m [32m"http://dmsgd.skywire.skycoin.com"[0m[1m,[0m
      [1m[94m"route_finder"[0m[1m:[0m [32m"http://rf.skywire.skycoin.com"[0m[1m,[0m
      [1m[94m"route_setup_nodes"[0m[1m:[0m [1m[[0m
        [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
        [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
      [1m][0m[1m,[0m
      [1m[94m"service_discovery"[0m[1m:[0m [32m"http://sd.skycoin.com"[0m[1m,[0m
      [1m[94m"stun_servers"[0m[1m:[0m [1m[[0m
        [32m"stun.l.google.com:19302"[0m
      [1m][0m[1m,[0m
      [1m[94m"transport_discovery"[0m[1m:[0m [32m"http://tpd.skywire.skycoin.com"[0m[1m,[0m
      [1m[94m"transport_setup"[0m[1m:[0m [1m[[0m
        [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m
      [1m][0m[1m,[0m
      [1m[94m"uptime_tracker"[0m[1m:[0m [32m"http://ut.skywire.skycoin.com"[0m
    [1m}[0m

Usage:
  skywire svc confbs



Flags:
  -a, --addr string               address to bind to
                                   (default ":9082")
  -c, --config string             stun server list file location
                                   (default "./config.json")
  -D, --dmsg-disc string          url of dmsg-discovery
                                   (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string   type of dmsg server on dmsghttp handler
      --dmsgPort uint16           dmsg port value
                                   (default 80)
  -d, --domain string             the domain of the endpoints
                                   (default "skywire.skycoin.com")
      --keyfile string            path to file containing secret key (auto-generated if missing)
                                  
      --mode string               listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string              address to bind pprof debug server (e.g. localhost:6060)
      --sk cipher.SecKey          dmsg secret key
                                   (default 0000000000000000000000000000000000000000000000000000000000000000)
      --tag string                logging tag
                                   (default "address_resolver")
```

## skywire svc ip

```
┌─┐┌─┐┌─┐┬┌─┐
│ ┬├┤ │ ││├─┘
└─┘└─┘└─┘┴┴  
GeoIP Service - looks up geographic location data for IP addresses.

Uses embedded MaxMind GeoLite2-City database by default.

HTTP Endpoints (API mode):
  GET  /             Lookup IP (from request or ?ip= param)
  GET  /?ip={ip}     Lookup specific IP address

Request/Response Examples:

GET /?ip=8.8.8.8
  [1m{[0m
      [1m[94m"ip_address"[0m[1m:[0m [32m"8.8.8.8"[0m[1m,[0m
      [1m[94m"latitude"[0m[1m:[0m [33m37.751[0m[1m,[0m
      [1m[94m"longitude"[0m[1m:[0m [33m-97.822[0m[1m,[0m
      [1m[94m"postal_code"[0m[1m:[0m [32m""[0m[1m,[0m
      [1m[94m"continent_code"[0m[1m:[0m [32m"NA"[0m[1m,[0m
      [1m[94m"continent_name"[0m[1m:[0m [32m"North America"[0m[1m,[0m
      [1m[94m"country_code"[0m[1m:[0m [32m"US"[0m[1m,[0m
      [1m[94m"country_name"[0m[1m:[0m [32m"United States"[0m[1m,[0m
      [1m[94m"region_code"[0m[1m:[0m [32m""[0m[1m,[0m
      [1m[94m"region_name"[0m[1m:[0m [32m""[0m[1m,[0m
      [1m[94m"city_name"[0m[1m:[0m [32m""[0m[1m,[0m
      [1m[94m"timezone"[0m[1m:[0m [32m"America/Chicago"[0m
    [1m}[0m

CLI: skywire svc ip 8.8.8.8
  (same response as above)

Usage Examples:
  skywire svc ip 8.8.8.8                              # CLI lookup
  skywire svc ip --api --addr ":9093"                 # API server (embedded DB)
  skywire svc ip --api --db ./GeoLite2-City.mmdb      # API server (external DB)

Usage:
  skywire svc ip



Flags:
  -a, --addr string     address to bind to
                         (default ":8080")
      --api             Run as API server
      --db string       Path to GeoLite2-City.mmdb database
  -l, --loglvl string   [info|error|warn|debug|trace|panic]
                         (default "info")
      --pprof string    address to bind pprof debug server (e.g. localhost:6060)
      --tag string      logging tag
                         (default "geoip")
```

## skywire svc nm

```
┌┐┌┌─┐┌┬┐┬ ┬┌─┐┬─┐┬┌─   ┌┬┐┌─┐┌┐┌┬┌┬┐┌─┐┬─┐
│││├┤  │ ││││ │├┬┘├┴┐───││││ │││││ │ │ │├┬┘
┘└┘└─┘ ┴ └┴┘└─┘┴└─┴ ┴   ┴ ┴└─┘┘└┘┴ ┴ └─┘┴└─
Network Monitor - monitors network health and deregisters stale services.

HTTP Endpoints:
  GET  /health     Health check
  GET  /status     Network status

Response Examples (from actual struct types):

GET /health - api.HealthCheckResponse
[1m{[0m
      [1m[94m"build_info"[0m[1m:[0m [1m{[0m
        [1m[94m"commit"[0m[1m:[0m [32m"abc1234"[0m[1m,[0m
        [1m[94m"date"[0m[1m:[0m [32m"2024-01-15T10:30:00Z"[0m[1m,[0m
        [1m[94m"version"[0m[1m:[0m [32m"v1.3.29"[0m
      [1m}[0m[1m,[0m
      [1m[94m"started_at"[0m[1m:[0m [32m"2024-01-15T10:00:00Z"[0m
    [1m}[0m

GET /status - nm.Status
[1m{[0m
      [1m[94m"last_update"[0m[1m:[0m [32m"2024-01-15T10:30:00Z"[0m[1m,[0m
      [1m[94m"online_visors"[0m[1m:[0m [33m1542[0m[1m,[0m
      [1m[94m"alive_transports"[0m[1m:[0m [33m3256[0m[1m,[0m
      [1m[94m"available_vpn"[0m[1m:[0m [33m128[0m[1m,[0m
      [1m[94m"available_skysocks"[0m[1m:[0m [33m256[0m[1m,[0m
      [1m[94m"available_public_visor"[0m[1m:[0m [33m512[0m[1m,[0m
      [1m[94m"last_cleaning"[0m[1m:[0m [1m{[0m
        [1m[94m"all_dead_entries_cleaned"[0m[1m:[0m [33m15[0m[1m,[0m
        [1m[94m"transport_discovery"[0m[1m:[0m [33m5[0m[1m,[0m
        [1m[94m"address_resolver"[0m[1m:[0m [1m{[0m
          [1m[94m"sudph"[0m[1m:[0m [33m0[0m[1m,[0m
          [1m[94m"stcpr"[0m[1m:[0m [33m0[0m
        [1m}[0m[1m,[0m
        [1m[94m"dmsg_discovery"[0m[1m:[0m [33m3[0m[1m,[0m
        [1m[94m"vpn"[0m[1m:[0m [33m2[0m[1m,[0m
        [1m[94m"skysocks"[0m[1m:[0m [33m3[0m[1m,[0m
        [1m[94m"public_visor"[0m[1m:[0m [33m2[0m
      [1m}[0m
    [1m}[0m

Usage:
  skywire svc nm

Available Commands:
  deregister              Deregister service(s) from service discovery

Flags:
  -a, --addr string          address to bind to
                              (default ":9080")
      --ar-url string        url to address resolver
                              (default "http://ar.skywire.skycoin.com")
  -d, --cleaning-delay int   time for delay between each service cleaning routine
                              (default 75)
      --dmsgd-url string     url to dmsg discovery
                              (default "http://dmsgd.skywire.skycoin.com")
  -l, --loglvl string        [info|error|warn|debug|trace|panic]
                              (default "info")
      --pk string            pk of network monitor
      --pprof string         address to bind pprof debug server (e.g. localhost:6060)
      --sd-url string        url to service discovery
                              (default "http://sd.skycoin.com")
      --sk string            sk of network monitor
      --tag string           logging tag
                              (default "network_monitor")
      --tpd-url string       url to transport discovery
                              (default "http://tpd.skywire.skycoin.com")
      --ut-url string        url to uptime tracker visor data
                              (default "http://ut.skywire.skycoin.com")
```

### skywire svc nm deregister

```

  Manually deregister one or more public keys from service discovery.

  By default, uses the local visor's RPC to perform deregistration. The visor's
  public key must be whitelisted as a network monitor in service discovery.

  Alternatively, use --sk to provide a secret key directly for signing the
  deregistration request (useful when running without a visor).

  Service types:
    - vpn      : VPN servers
    - visor    : Public visors
    - skysocks : SOCKS5 proxy servers (alias: proxy)

  Examples:
    # Deregister a VPN server using visor RPC (default)
    skywire svc nm deregister --pk <public-key> --type vpn

    # Deregister using a specific visor RPC address
    skywire svc nm deregister --pk <public-key> --type vpn --rpc localhost:3435

    # Deregister multiple keys from all service types
    skywire svc nm deregister --pk <pk1>,<pk2> --all-types

    # Deregister using a secret key directly (without visor)
    skywire svc nm deregister --pk <public-key> --type visor --sk <secret-key>

    # Deregister from a specific service discovery instance (with --sk)
    skywire svc nm deregister --pk <public-key> --type visor --sk <secret-key> --sd-url http://localhost:9098

Usage:
  skywire svc nm deregister



Flags:
  -a, --all-types       deregister from all service types (vpn, visor, skysocks)
  -p, --pk string       public key(s) to deregister (comma-separated for multiple)
  -r, --rpc string      visor RPC address (used when --sk is not provided)
                         (default "localhost:3435")
      --sd-url string   service discovery URL (only used with --sk)
                         (default "http://sd.skycoin.com")
      --sk string       secret key for signing (if not provided, uses visor RPC)
  -t, --type string     service type: vpn, visor, skysocks (or proxy)
```

## skywire svc rf

```
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

Usage:
  skywire svc rf



Flags:
  -a, --addr string               address to bind to
                                   (default ":9092")
  -D, --dmsg-disc string          url of dmsg discovery
                                   (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string   type of dmsg server on dmsghttp handler
      --dmsgPort uint16           dmsg port value
                                   (default 80)
      --keyfile string            path to file containing secret key (auto-generated if missing)
                                  
  -l, --loglvl string             [info|error|warn|debug|trace|panic]
                                   (default "info")
  -m, --metrics string            address to bind metrics API to
      --mode string               listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string              address to bind pprof debug server (e.g. localhost:6060)
      --redis string              connections string for a redis store
                                   (default "redis://localhost:6379")
      --redis-pool-size int       redis connection pool size
                                   (default 10)
      --sk cipher.SecKey          dmsg secret key
                                   (default 0000000000000000000000000000000000000000000000000000000000000000)
      --tag string                logging tag
                                   (default "route_finder")
  -t, --testing                   enable testing to start without redis
```

## skywire svc sd

```
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

Usage:
  skywire svc sd



Flags:
  -a, --addr string               address to bind to
                                   (default ":9098")
  -d, --dmsg-disc string          url of dmsg-discovery
                                   (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string   type of dmsg server on dmsghttp handler
      --dmsgPort uint16           dmsg port value
                                   (default 80)
      --entry-timeout duration    client service entry TTL (0 to disable)
                                   (default 5m0s)
      --geoip string              url of geoip service
                                   (default "http://ip.skycoin.com")
      --keyfile string            path to file containing secret key (auto-generated if missing)
                                  
  -m, --metrics string            address to bind metrics API to
      --mode string               listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string              address to bind pprof debug server (e.g. localhost:6060)
  -r, --redis string              connections string for a redis store
                                   (default "redis://localhost:6379")
  -s, --sk cipher.SecKey          dmsg secret key
                                   (default 0000000000000000000000000000000000000000000000000000000000000000)
  -t, --test                      run in test mode and disable auth
  -w, --whitelist-keys string     list of whitelisted keys of network monitor used for deregistration
```

## skywire svc se

```
┌─┐┬ ┬   ┌─┐┌┐┌┬  ┬
└─┐│││───├┤ │││└┐┌┘
└─┘└┴┘   └─┘┘└┘ └┘

Usage:
  skywire svc se

Available Commands:
  dmsg                    Generate config for dmsg-server
  setup                   Generate config for setup node
  visor                   Generate config for skywire-visor

Flags:
  -d, --docker           Environment with dockerized skywire-services
  -l, --local            Environment with skywire-services on localhost
  -n, --network string   Docker network to use
                          (default "SKYNET")
  -p, --public           Environment with public skywire-services
```

### skywire svc se dmsg

```
Generate config for dmsg-server

Usage:
  skywire svc se dmsg


```

### skywire svc se setup

```
Generate config for setup node

Usage:
  skywire svc se setup


```

### skywire svc se visor

```
Generate config for skywire-visor

Usage:
  skywire svc se visor


```

## skywire svc sn

```
┬─┐┌─┐┬ ┬┌┬┐┌─┐   ┌─┐┌─┐┌┬┐┬ ┬┌─┐   ┌┐┌┌─┐┌┬┐┌─┐
├┬┘│ ││ │ │ ├┤ ───└─┐├┤  │ │ │├─┘───││││ │ ││├┤ 
┴└─└─┘└─┘ ┴ └─┘   └─┘└─┘ ┴ └─┘┴     ┘└┘└─┘─┴┘└─┘
Route Setup Node - establishes routes between visors via dmsg RPC.

Listens on dmsg port 36 for route setup requests from visors.

RPC Methods (via dmsg):
  SetupRPCGateway.DialRouteGroup    Establish bidirectional route
  SetupRPCGateway.HealthCheck       Health check

Example Config:
  [1m{[0m
      [1m[94m"public_key"[0m[1m:[0m [32m"000000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"secret_key"[0m[1m:[0m [32m"0000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"dmsg"[0m[1m:[0m [1m{[0m
        [1m[94m"discovery"[0m[1m:[0m [32m"http://dmsgd.skywire.skycoin.com"[0m[1m,[0m
        [1m[94m"sessions_count"[0m[1m:[0m [33m1[0m[1m,[0m
        [1m[94m"servers"[0m[1m:[0m [2mnull[0m[1m,[0m
        [1m[94m"servers_type"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"protocol"[0m[1m:[0m [32m""[0m
      [1m}[0m[1m,[0m
      [1m[94m"transport_discovery"[0m[1m:[0m [32m"http://tpd.skywire.skycoin.com"[0m[1m,[0m
      [1m[94m"log_level"[0m[1m:[0m [32m""[0m
    [1m}[0m

Generate Keys:
  skywire cli config gen-keys | tee sn-keys.txt
  # Line 1: public_key, Line 2: secret_key

Usage:
  skywire svc sn [config.json]
  skywire cli config gen --sn -o sn-config.json
  skywire cli config gen --sn | skywire svc sn -i

Usage:
  skywire svc sn

Available Commands:
  health                  Health check of route setup node

Flags:
  -m, --metrics string     address to bind metrics API to
  -r, --pprofaddr string   pprof http port (default "localhost:6060")
  -q, --pprofmode string   [ http ] pprof mode
  -i, --stdin              read config from STDIN
      --tag string         logging tag
                            (default "setup_node")
```

### skywire svc sn health

```
Health check of route setup node

Usage:
  skywire svc sn health <pk>


```

## skywire svc stun

```
┌─┐┌┬┐┬ ┬┌┐┌   ┌─┐┌─┐┬─┐┬  ┬┌─┐┬─┐
└─┐ │ │ ││││───└─┐├┤ ├┬┘└┐┌┘├┤ ├┬┘
└─┘ ┴ └─┘┘└┘   └─┘└─┘┴└─ └┘ └─┘┴└─

STUN server implementing RFC 3489 NAT discovery.
Requires two distinct IPs for full NAT type detection.

  skywire svc stun --primary-ip 139.162.160.227 --secondary-ip 172.104.247.120
  skywire svc stun --primary-ip 127.0.0.1 --secondary-ip 127.0.0.2 --port 3478 --alt-port 3479

Usage:
  skywire svc stun



Flags:
      --alt-port int          alternate STUN port
                               (default 3479)
  -l, --loglvl string         [info|error|warn|debug|trace|panic]
                               (default "info")
      --port int              primary STUN port
                               (default 3478)
      --primary-ip string     primary listening IP (required)
      --secondary-ip string   secondary listening IP (required)
      --tag string            logging tag
                               (default "stun")
```

## skywire svc tpd

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

Usage:
  skywire svc tpd



Flags:
  -a, --addr string               address to bind to
                                   (default ":9091")
      --dmsg-disc string          url of dmsg-discovery
                                   (default "http://dmsgd.skywire.skycoin.com")
      --dmsg-server-type string   type of dmsg server on dmsghttp handler
      --dmsgPort uint16           dmsg port value
                                   (default 80)
      --entry-timeout duration    transport entry TTL (0 to disable)
                                   (default 5m0s)
      --keyfile string            path to file containing secret key (auto-generated if missing)
                                  
  -l, --loglvl string             [info|error|warn|debug|trace|panic]
                                   (default "info")
  -m, --metrics string            address to bind metrics API to
      --mode string               listener mode: http|dmsg|dual (default dual if --sk, else http; env SKYWIRE_SVC_MODE overrides)
      --pprof string              address to bind pprof debug server (e.g. localhost:6060)
      --redis string              connections string for a redis store
                                   (default "redis://localhost:6379")
      --redis-pool-size int       redis connection pool size
                                   (default 10)
      --sk cipher.SecKey          dmsg secret key
                                   (default 0000000000000000000000000000000000000000000000000000000000000000)
      --store-data-path string    path for bandwidth backup files
                                   (default "/var/lib/skywire/tpd/bandwidth")
      --tag string                logging tag
                                   (default "transport_discovery")
      --test-environment          distinguished between prod and test environment
  -t, --testing                   enable testing to start without redis
      --whitelist-keys string     list of whitelisted keys of network monitor used for deregistration
```

## skywire svc tps

```
┌┬┐┬─┐┌─┐┌┐┌┌─┐┌─┐┌─┐┬─┐┌┬┐   ┌─┐┌─┐┌┬┐┬ ┬┌─┐
 │ ├┬┘├─┤│││└─┐├─┘│ │├┬┘ │ ───└─┐├┤  │ │ │├─┘
 ┴ ┴└─┴ ┴┘└┘└─┘┴  └─┘┴└─ ┴    └─┘└─┘ ┴ └─┘┴  
Transport Setup Node - remotely manages transports on visors via dmsg RPC.

HTTP Endpoints:
  POST /{pk}/transports    List transports on visor
  POST /add                Add transport between two visors
  POST /remove             Remove transport from visor

Request/Response Examples:

POST /add
  Request:  [1m{[0m
      [1m[94m"from"[0m[1m:[0m [32m"000000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"to"[0m[1m:[0m [32m"000000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"type"[0m[1m:[0m [32m"stcpr"[0m
    [1m}[0m
  Response: {"id": "e7a7f1b3-c040-47f8-9e12-a0a1459b3456", "type": "stcpr", ...}

POST /remove
  Request:  [1m{[0m
      [1m[94m"from"[0m[1m:[0m [32m"000000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"id"[0m[1m:[0m [32m"00000000-0000-0000-0000-000000000000"[0m
    [1m}[0m
  Response: {"success": true}

GET /{pk}/transports
  [1m[[0m
      [1m{[0m
        [1m[94m"edges"[0m[1m:[0m [1m[[0m
          [32m"02a49bc0aa1b5b78f638e9189be4c5d699e6d1358472d8a47f4c20daacd672d7e5"[0m,
          [32m"03b160fa44bac22cae9f7eb1311f1648aaab962e1e55d8d9a22a9586ded871eb5e"[0m
        [1m][0m[1m,[0m
        [1m[94m"id"[0m[1m:[0m [32m"e7a7f1b3-c040-47f8-9e12-a0a1459b3456"[0m[1m,[0m
        [1m[94m"type"[0m[1m:[0m [32m"stcpr"[0m
      [1m}[0m
    [1m][0m

Example Config:
[1m{[0m
      [1m[94m"public_key"[0m[1m:[0m [32m"000000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"secret_key"[0m[1m:[0m [32m"0000000000000000000000000000000000000000000000000000000000000000"[0m[1m,[0m
      [1m[94m"port"[0m[1m:[0m [33m8080[0m[1m,[0m
      [1m[94m"dmsg"[0m[1m:[0m [1m{[0m
        [1m[94m"discovery"[0m[1m:[0m [32m"http://dmsgd.skywire.skycoin.com"[0m[1m,[0m
        [1m[94m"sessions_count"[0m[1m:[0m [33m2[0m[1m,[0m
        [1m[94m"servers"[0m[1m:[0m [2mnull[0m[1m,[0m
        [1m[94m"servers_type"[0m[1m:[0m [32m""[0m[1m,[0m
        [1m[94m"protocol"[0m[1m:[0m [32m""[0m
      [1m}[0m
    [1m}[0m

Generate Keys:
  skywire cli config gen-keys | tee tps-keys.txt
  # Line 1: public_key, Line 2: secret_key

Usage:
  skywire svc tps

Available Commands:
  add                     add transport to remote visor
  list                    list transports of remote visor
  rm                      remove transport from remote visor

Flags:
  -c, --config string   path to config file
  -l, --loglvl string   [info|error|warn|debug|trace|panic]
                         (default "debug")
```

### skywire svc tps add

```
add transport to remote visor

Usage:
  skywire svc tps add



Flags:
  -1, --from string   PK to request transport setup
  -2, --to string     other transport edge PK
  -t, --type string   transport type to request creation of [stcpr|sudph|dmsg]
  -p, --pretty        pretty print result
  -z, --addr string   address of the transport setup-node
                       (default "http://127.0.0.1:8080")
```

### skywire svc tps list

```
list transports of remote visor

Usage:
  skywire svc tps list



Flags:
  -1, --from string   PK to request transport list
  -p, --pretty        pretty print result
  -z, --addr string   address of the transport setup-node
                       (default "http://127.0.0.1:8080")
```

### skywire svc tps rm

```
remove transport from remote visor

Usage:
  skywire svc tps rm



Flags:
  -1, --from string   PK to request transport takedown
  -i, --tpid string   id of transport to remove
  -p, --pretty        pretty print result
  -z, --addr string   address of the transport setup-node
                       (default "http://127.0.0.1:8080")
```

## skywire svc ut

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

Usage:
  skywire svc ut



Flags:
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

