# Service Discovery

| Status   | Implemented                                                              |
|----------|--------------------------------------------------------------------------|
| Revision | 2026-10-27, skywire 38152beed                                            |
| Code     | `pkg/deployment/sd`, `pkg/services/sd`, `pkg/servicedisc`                |

## Abstract

Service Discovery (SD) is a registry of the services that visors offer. A visor
registers entries for the services it runs, such as a VPN server, a proxy, a
public visor entry or a coin node, and clients query the registry by service
type. SD also tracks visor uptime.

## Overview

Visors register and refresh their own entries. Clients such as the VPN client,
the proxy client and autoconnect list entries by type and choose among them.
The Network Monitor removes entries of visors that are no longer online.
Entries are also available as a CXO feed, which visors prefer to polling the
HTTP API (see Public Autoconnect).

## Addressing

SD is served over dmsg HTTP at `dmsg://<sd-pk>:80`. A service entry is
addressed by `servicedisc.SWAddr`, the text form `<public-key>:<port>`. A
missing port reads as 0.

## Data Formats

### Service

`servicedisc.Service`, the request body of `POST /api/services` and the
response element of the read routes.

| JSON key          | Type     | Required | Meaning                                          |
|-------------------|----------|----------|--------------------------------------------------|
| `address`         | string   | yes      | `SWAddr` of the service                          |
| `type`            | string   | yes      | service type                                     |
| `geo`             | object   | no       | `lat`, `lon`, `country`, `region` (`geo.LocationData`) |
| `display_node_ip` | bool     | no       | whether `local_ips` is returned to readers       |
| `version`         | string   | for `vpn`, `skysocks` | visor version                       |
| `local_ips`       | []string | for `visor` | addresses of the visor; must contain the observed source address |
| `info`            | object   | no       | `servicedisc.VPNInfo`: `latency`, `uptime`, `connections` |
| `coin`            | object   | no       | `servicedisc.CoinInfo`, for type `coin`          |

`Service.Check` requires a non-null key and a non-zero port.

### Service types

| Constant                       | Value      |
|--------------------------------|------------|
| `servicedisc.ServiceTypeVisor` | `visor`    |
| `servicedisc.ServiceTypeVPN`   | `vpn`      |
| `servicedisc.ServiceTypeSkysocks` | `skysocks` |
| `servicedisc.ServiceTypeProxy` | `proxy`, an alias of `skysocks` on the read routes |
| `servicedisc.ServiceTypeCoin`  | `coin`     |

### Errors

`servicedisc.HTTPError`: `{"code": <status>, "error": "<text>"}`.

### Uptimes

`store.VisorSummary`.

| JSON key   | Type   | Meaning                                              |
|------------|--------|------------------------------------------------------|
| `pk`       | string | visor key                                            |
| `on`       | bool   | whether the visor currently has a live registration  |
| `version`  | string | from `v=v2` and `v=v3`                               |
| `daily`    | object | day to uptime percentage, from `v=v2` and `v=v3`     |
| `timeline` | object | day to a 288 character bitmap, from `v=v3`           |

## Interfaces

### HTTP API

The request body limit is 1 MiB and each request has a 30 s timeout
(`httpTimeout`). CORS allows any origin. Routes marked auth use
`httpauth.MakeMiddleware` when SD has a nonce store (see HTTP Authorization
Middleware).

| Method and path                         | Auth | Purpose                              |
|-----------------------------------------|------|--------------------------------------|
| `GET /api/services`                     | no   | list entries of a type               |
| `GET /api/services/{addr}`              | no   | get one entry                        |
| `POST /api/services`                    | yes  | register or refresh an entry         |
| `DELETE /api/services/{addr}`           | yes  | remove an entry                      |
| `DELETE /api/services/deregister/{type}`| NM   | remove entries of the listed keys    |
| `GET /uptimes`, `POST /uptimes`         | no   | visor uptimes                        |
| `GET /health`                           | no   | `httputil.HealthCheckResponse`, service name `service-discovery` |
| `GET /`                                 | no   | status page                          |
| `GET /security/nonces/{pk}`             | no   | next nonce, when a nonce store exists |

`GET /api/services` requires the query parameter `type`. Optional parameters
are `version` and `country`, which filter exactly, and `quantity`, which returns
a random sample of that many entries when more match. Local addresses are
removed from entries that do not set `display_node_ip`. Responses are 200 with
an array (empty when nothing matches), 400 when `type` is missing or `quantity`
is not an integer, and 500 on a store error.

`GET /api/services/{addr}?type=<type>` returns 200 with one entry, 400 when
`type` is missing or `addr` does not parse, 404 when the entry does not exist,
and 500 on a store error.

`POST /api/services` stores the entry and returns 200 with the stored entry.
The response status follows the order of checks.

- 400 when the body is not valid JSON.
- 500 for a `visor` entry when no geo data is found for the observed address.
  Other types register without geo data.
- 403 when the entry key differs from the authenticated key.
- 403 for `vpn` and `skysocks` with an empty `version`.
- 403 for `visor` when the observed address is not in `local_ips`.
- 500 on a store error.

`DELETE /api/services/{addr}?type=<type>` returns 200 with `true`, 400 when
`type` is missing or `addr` does not parse, 403 on a key mismatch, and 500 on a
store error.

`DELETE /api/services/deregister/{type}` is called by the Network Monitor. It
does not use the nonce middleware. The caller sends `NM-PK` and `NM-Sign`.
`NM-PK` must be in `api.WhitelistPKs` (`nmpk.GetWhitelistPKs`). `NM-Sign` must
be a signature by `NM-PK` over the text of `NM-PK`. The body is a JSON array of
public key strings, and each key is removed from the entries of `{type}` with
port 0. Responses are 200 with `true`, 400 for an unparsable key, signature or
body, 403 for a key outside the whitelist or a bad signature, and 500 on a
store error.

`GET /uptimes` returns an array of `VisorSummary` from a cache refreshed every
5 minutes (`uptimesCacheDelay`). The parameter `v` selects `v2` or `v3`. The
parameter `visors` is a semicolon separated key list that filters the result. The
`v3` form computes timelines on request. `POST /uptimes` takes
`{"pks":[...],"v":"v2"}` and answers the same way, with 400 for an invalid body.

### CXO Feeds

| Feed                 | Port | Publisher | Constant                       |
|----------------------|------|-----------|--------------------------------|
| services             | 53   | SD        | `skyenv.DmsgSDServicesCXOPort` |
| visor registration   | 72   | each visor | `skyenv.DmsgVisorSDRegCXOPort` |

Services feed. The leaf `services/<type>/all` holds all live entries of a
type. The body is a version byte (`servicesBatchVersion`, 1) and a gzip frame of
a JSON array of `Service`, sorted by key. An entry that is deregistered or has
expired is absent from the next version of its leaf. The tree is rebuilt from the
store every 60 s (`servicesCXORepublishInterval`) and on each registration or
removal.

Visor registration feed. A visor publishes its own entries as the leaf
`services`, in the same framing, and announces the feed to SD. SD subscribes
back (`regcxo.Aggregator`) and passes each entry to `IngestServiceFromCXO`. An
entry whose key is not the publisher's is dropped. When an entry exists, the
ingest refreshes its TTL and keeps the stored value. When none exists, entries
of type `visor` are not created, and others are created if `vpn` and `skysocks`
entries carry a version. The feed has no delete. An entry the visor stops
publishing expires.

## Behaviour

### Store and lifetime

Entries are Redis keys `service:<type>:<pk>` with a TTL equal to the service
`entry_timeout` setting, 5 minutes by default (`cmd/svc/service-discovery`).
A set `service:<type>` indexes the keys of a type, and a set
`sd:visor-svc:<pk-hex>` lists the types a visor has registered. Visors refresh
entries every 90 s (`skyenv.ServiceDiscUpdateInterval`). Index members whose key
has expired are removed when read and by a cleanup that runs every half TTL.

Each registration also records a heartbeat for the visor. The uptime counters
have a 8 day expiry. A visor counts as online while it holds a live
registration.

## Security Considerations

Registration and removal are authenticated, and a visor can only register or
remove entries under its own key. Reads are open. A `visor` entry is accepted
only when the observed source address is in its `local_ips`. Entries expose
`local_ips` only when the registering visor sets `display_node_ip`.

## References

- HTTP Authorization Middleware
- Network Monitor
- Public Visors
- Public Autoconnect
