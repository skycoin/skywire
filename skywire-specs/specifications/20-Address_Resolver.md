# Address Resolver

| Status   | Implemented                                                                                              |
|----------|----------------------------------------------------------------------------------------------------------|
| Revision | 2026-10-27, skywire c18b044dc |
| Code     | `pkg/deployment/ar`, `pkg/services/ar`, `pkg/transport/network/addrresolver`, `pkg/visor/reach_card.go` |

## Abstract

The Address Resolver (AR) maps visor public keys to the network addresses at
which a visor accepts direct transports. Visors register addresses for the
`stcpr`, `sudph`, `squicr` and `swtr` transport types, and look up a peer's
address before dialing it. The AR also holds the UDP control connection that
`sudph` needs, and publishes its bindings and reachability data as CXO feeds.

## Overview

A visor binds its addresses at the AR, directly or through the visor bind CXO
feed. A dialing visor obtains a peer's address from three sources.

- The peer's own signed reach card, `GET /reach` on the peer.
- The bindings feed the visor holds from the AR (CXO port 74).
- `GET /resolve/{type}/{pk}` on the AR.

The first two form the resolve fast path that the visor installs in the AR
client with `SetResolveFastPath` (`Visor.resolveFast`). It uses a card the visor
already holds for the peer, otherwise the bindings feed, and otherwise it fetches
the card. When the fast path has no answer, the client asks the AR.

`sudph` is always resolved at the AR, because the AR also asks the peer to dial
back (`FastResolve` is never asked for `sudph`).

## Addressing

The HTTP API is served over dmsg HTTP at `dmsg://<ar-pk>:80`. The `sudph`
control connection is a KCP listener on UDP. Its address is the `udp_addr`
service setting, `:30178` when empty (`pkg/services/ar`). The address that
visors reach is advertised as `udp_address` in `GET /health` when the AR is
configured with `public_udp_addr`.

## Data Formats

### Bind body

`addrresolver.LocalAddresses`, the body of `POST /bind/{stcpr,quic,wt}` and the
first message and every re-registration message on the `sudph` control
connection.

| JSON key         | Type     | Required | Meaning                                              |
|------------------|----------|----------|------------------------------------------------------|
| `port`           | string   | yes      | listening port                                       |
| `addresses`      | []string | yes      | local interface addresses; the observed source must be in it for `POST /bind/*` |
| `public_ip`      | string   | no       | public IPv4, used when the observed source is not public |
| `public_ip_v6`   | string   | no       | public IPv6, used only if it is a public IPv6 address |
| `cert_hash`      | string   | no       | SHA-256 hex of the WebTransport certificate, set by `bind/wt` |
| `cert_hash_next` | string   | no       | hash of the certificate after the next rotation      |

### Resolve response

`addrresolver.VisorData`, the stored record and the body of `GET /resolve`.

| JSON key         | Type   | Meaning                                                     |
|------------------|--------|-------------------------------------------------------------|
| `remote_addr`    | string | IPv4 `host:port` as seen by the AR, or the declared address |
| `remote_addr_v6` | string | IPv6 address, omitted when empty                            |
| `is_local`       | bool   | set on resolve when the caller shares the target's IP       |
| `port`, `addresses`, `public_ip`, `public_ip_v6`, `cert_hash`, `cert_hash_next` | | the embedded `LocalAddresses` |

### Other formats

`api.ArData` is the body of `GET /transports`: `{"sudph":[pk...],"stcpr":[pk...]}`.
`api.Error` is `{"error":"<text>"}`.

The `sudph` control connection carries these messages.

| Direction     | Message                                  | Type                              |
|---------------|------------------------------------------|-----------------------------------|
| visor to AR   | local addresses                          | `LocalAddresses` JSON             |
| visor to AR   | `heartbeat`                              | `UDPKeepHeartbeatMessage`, echoed by the AR |
| visor to AR   | `delBind`                                | `UDPDelBindMessage`               |
| visor to AR   | lookup `{"resolve":"<pk>","id":n}`       | `UDPResolveRequest`               |
| AR to visor   | `{"resolved":n,"found":b,"data":{},"err":""}` | `UDPResolveReply`            |
| AR to visor   | `{"PK":"<pk>","Addr":"<host:port>"}`     | `RemoteVisor`, a request to dial  |

## Interfaces

### HTTP API

Routes marked yes use `httpauth.MakeMiddleware` (see HTTP Authorization
Middleware). The route marked list accepts only keys in the deregistration
whitelist. The request body limit is 1 MiB (`maxBodyBytes`).

| Method and path               | Auth | Purpose                                         |
|-------------------------------|------|-------------------------------------------------|
| `POST /bind/stcpr`            | yes  | bind the caller's `stcpr` address               |
| `DELETE /bind/stcpr`          | yes  | remove the caller's `stcpr` binding             |
| `POST /bind/quic`             | yes  | bind the caller's `squicr` address              |
| `POST /bind/wt`               | yes  | bind the caller's `swtr` address and cert hash  |
| `GET /resolve/{type}/{pk}`    | yes  | resolve a peer                                  |
| `GET /health`                 | no   | `httputil.HealthCheckResponse`, service name `address-resolver`, with `udp_address` |
| `GET /sudph-sessions`         | no   | KCP session counters, `?closed=N` lists N closed addresses |
| `GET /transports`             | no   | `ArData`, gzip above the compression threshold  |
| `GET /`                       | no   | status page                                     |
| `DELETE /deregister/{network}`| list | remove bindings, see below                      |
| `GET /security/nonces/{pk}`   | no   | next nonce for `pk` (`httpauth.NonceHandler`)   |

The caller of an authenticated route is the authenticated key. A visor can only
bind or delete its own record.

`POST /bind/{stcpr,quic,wt}`

- The observed source address must be public. If it is not, and `public_ip` is
  a public address, `public_ip` replaces it. Otherwise the answer is 400.
- The observed address, after any replacement, must appear in `addresses`.
  Otherwise the answer is 400.
- A public `public_ip_v6` is stored as `remote_addr_v6`.
- A bind of a second address family merges into the stored record.
- Responses are 200 with an empty body, 400 with `Error`, 401 when the request
  carries no authenticated key, and 500 when the store fails. A body that is not
  valid JSON receives no explicit status and so the implicit 200 with an empty
  body, and nothing is stored.

`DELETE /bind/stcpr` returns 200 with an empty body, 401, or 500.

`GET /resolve/{type}/{pk}`

- `type` is a transport type name. Legacy aliases are normalised
  (`types.NormalizeType`).
- Responses are 200 with `VisorData`, 400 when `pk` does not parse, 401, 404
  when there is no entry or the type is not stored (for example `webrtc`), and
  503 with `Retry-After: 2` and `Error` when the store is unavailable.
- For `sudph` the AR also sends `RemoteVisor` for the caller to the target over
  the target's control connection, after the response. A target without a live
  control connection is skipped silently.

`DELETE /deregister/{network}` removes the bindings of the keys in the body, a
JSON array of public key strings, for `{network}` of `stcpr` or `sudph`. It is
the bulk endpoint for removing other visors' entries.
The caller is authorized by the deregistration whitelist (see
[02](02-HTTP_Authorization_Middleware.md#deregistration-whitelist)). Responses are 200 with body `null`, 400 for an unparsable key,
signature, network or body, 403 for a key outside the whitelist or a bad
signature, and 500 when a delete fails.

### SUDPH over the UDP control connection

`sudph` has no HTTP bind route. A visor opens a KCP connection to the AR UDP
address and the AR runs the responder side of the handshake
(`handshake.ResponderHandshake`, 5 s timeout `sudphHandshakeTimeout`). At most
256 handshakes are in flight (`sudphMaxInFlightHandshakes`); further
connections are closed. The authenticated key is the handshake peer.

After the handshake the visor sends `LocalAddresses` within 30 s
(`sudphInitialReadTimeout`). The AR binds the key to the observed UDP source.
If that source is not public and the message declares a public `public_ip` and
`port`, the declared pair is used instead (`effectiveSUDPHAddr`). One
connection per key is kept; a new connection replaces the old one.

The AR closes a connection that is silent for 3 minutes (`sudphReadTimeout`).
The visor sends `heartbeat` every 10 s (`udpKeepHeartbeatInterval`) and the AR
echoes it. The visor sends `LocalAddresses` again every 90 s
(`sudphReRegisterInterval`), which rebinds and refreshes the entry. `delBind`
removes the binding and ends the connection.

### CXO Feeds

| Feed                   | Port | Publisher | Constant                       |
|------------------------|------|-----------|--------------------------------|
| visor bind feed        | 71   | each visor | `skyenv.DmsgVisorARBindCXOPort` |
| merged bindings        | 74   | AR        | `skyenv.DmsgARBindingsCXOPort` |
| reach summary          | 76   | AR        | `skyenv.DmsgARReachCXOPort`    |

Visor bind feed. A visor publishes its bind payloads as a CXO feed and
announces it to the AR. The AR (`regcxo.Aggregator`) subscribes back and reads
the leaves `stcpr`, `sudph`, `squicr` and `swtr`, each a `LocalAddresses`
JSON, and the leaf `reach`, an `arfeed.ReachDecl` (`nat`, `acc`, `in`). A bind is
stored under the publishing key. When a record exists, the ingest rewrites the
stored record to refresh its TTL and does not overwrite the address. When none
exists, the types `stcpr`, `squicr` and `swtr` are created from the declared
addresses and `sudph` is not (`IngestBindFromCXO`). HTTP and UDP binds remain
authoritative.

Merged bindings feed (`arfeed`). A tree of 256 buckets at path
`<h2>/<h3>`, where `h2` and `h3` are the third and fourth hex characters of the
public key. Each leaf is a version byte (`arfeed.Version`, 1) and a gzip frame
of a JSON map from public key hex to `arfeed.PeerBindings`, an object with the
optional members `stcpr`, `sudph`, `squicr` and `swtr`, each a `VisorData`. All
256 leaves are always published. The publisher marks a bucket when a binding
changes and flushes after 3 s (`bindingsFlushWindow`), publishes the root after
a 30 s batch window (`bindingsBatchWindow`), and rebuilds the whole tree every 5
minutes (`bindingsResyncInterval`).

Reach summary feed. A tree of 16 leaves at `reach/<h>`, `h` being the first hex
digit after the `02` or `03` prefix. A leaf is a version byte
(`arfeed.ReachVersion`) and a gzip frame of a map from public key hex to
`arfeed.Reach`. A record holds the visor's declaration (`nat`, `acc`, `in`) and
what the AR observed: `open` and `closed` types from the AR's own probe of the
bound address, `udp` for a live control connection, and `bound` types. The AR
probes `stcpr`, `squicr` and `swtr` addresses, re-probing a binding that
answered every 30 minutes and one that did not every 5 minutes
(`probeEvery`, `probeRetry`); a binding is closed after 2 failed probes
(`probeFailsToClose`). A declaration expires after 10 minutes (`declTTL`).

## Behaviour

### Entry lifetime

Each binding is a Redis key `address-resolver:<type>:<pk>` with a TTL equal to
the service `entry_timeout`, 5 minutes by default (`cmd/svc/address-resolver`).
Each bind, `sudph` re-registration and CXO keepalive resets the TTL. A visor
re-registers `stcpr`, `squicr` and `swtr` every 90 s. A per type Redis set
indexes the keys. `GetAll` checks that each member exists and removes the ones
that do not, and its answer is cached for 5 seconds (`defaultGetAllCacheTTL`).

### Deregistration

A binding is removed in one of four ways.

- A visor removes its own `stcpr` binding with the authenticated
  `DELETE /bind/stcpr`.
- A visor removes its `sudph` binding by sending `delBind` on the control
  connection when it shuts down (`addrresolver.UDPDelBindMessage`).
- The binding expires when its TTL runs out.
- A key in the deregistration whitelist removes entries of other visors with
  `DELETE /deregister/{network}`.

### Reach cards

Each visor serves its own signed reach card at `GET /reach` (`addrresolver.ReachPath`),
on its log server over dmsg and skynet. The card (`addrresolver.ReachCard`)
holds the records the AR holds for the visor, one per type, so that peers can
dial it without asking the AR.

| JSON key  | Type   | Meaning                                                  |
|-----------|--------|----------------------------------------------------------|
| `pk`      | string | owner                                                    |
| `at`      | int    | unix seconds when built                                  |
| `records` | JSON   | map from transport type to `VisorData`, kept as signed   |
| `sig`     | string | signature over `pk`, `at` as 8 bytes big endian, then `records` |

The records are the visor's own resolve results for `stcpr`, `sudph`, `squicr`
and `wt` (`arSelfTypes`), refreshed from the AR when older than 5 minutes
(`arSelfMaxAge`) or when a bind payload changes. A card is rebuilt at most
every 30 s (`reachCardBodyKeep`). The response is 200 with `Cache-Control:
max-age=60`, 404 when the visor does not advertise itself (`ar_transport_limit`
below 0, `logserver.ErrNotReachable`), and 503 when no records exist yet.

A dialer fetches a card with `GET /reach` at `dmsg://<pk>:80`, with a 3 s
timeout and a 64 KiB body limit. It accepts the card only if `ReachCard.Verify`
succeeds for that key. A card is held for 2 minutes, and a peer that served none
is not asked again for 5 minutes (`reachCardKeep`, `reachCardMissKeep`). When a
fresh card lacks the requested type, the dialer uses the bindings feed and then
`GET /resolve`.

## Security Considerations

Bind and resolve requests are authenticated by `httpauth`. Over dmsg the
caller's key is the authenticated dmsg peer. A visor cannot bind another key.
The CXO bind ingest stores a binding only under the publisher's key.

`GET /health`, `/transports` and `/sudph-sessions` are unauthenticated, so the
list of bound keys is public. `DELETE /deregister/{network}` requires a
whitelisted key and a signature by it. A reach card is signed by its owner, so a
peer that relays or caches it cannot alter it.

## References

- HTTP Authorization Middleware
- Address Resolver Privacy
- Transport Discovery
