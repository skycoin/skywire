# Dmsg

| Status   | Implemented                                                              |
|----------|--------------------------------------------------------------------------|
| Revision | 2026-10-27, skywire c18b044dc |
| Code     | `pkg/dmsg/dmsg`, `pkg/dmsg/disc`, `pkg/dmsg/discovery`, `pkg/dmsg/noise`, `pkg/dmsg/dmsgc/spec` |

## Abstract

Dmsg is the distributed messaging system that gives every visor and service a
dialable address, `<public key>:<port>`, without a public IP. Clients keep
sessions to dmsg servers. A server relays end to end encrypted streams between
the clients it holds. A discovery service maps client keys to the servers they
use and lists the servers.

## Overview

There are three roles.

- A **client** (`dmsg.Client`) holds one or more sessions to servers, registers
  a signed entry in discovery that names those servers, listens on ports, and
  dials other clients.
- A **server** (`dmsg.Server`) accepts sessions and bridges a stream request
  from one client session to the session of the destination client. It forwards
  to a peer server when the destination is not local.
- The **discovery** (`/dmsg-discovery`) stores signed entries.

A stream between two clients carries its own Noise handshake, so a server
relays ciphertext only. Deployment services are dmsg clients that listen on
port 80 for HTTP.

## Addressing

`dmsg.Addr` is a public key and a 16 bit port, written `<pk>:<port>`. Port 0 is
written `~`. Ports in `pkg/skyenv` are listed in [01](01-Overview.md). Discovery
is served at `dmsg://<dmsg-discovery-pk>:80`, key `dmsg_discovery_dmsg` in
`deployment.Services`.

## Data Formats

### Discovery entry

`disc.Entry` (`pkg/dmsg/disc/entry.go`). Servers and clients use the same
structure, with exactly one of `client` and `server` set for a registered
entry. The signature covers the JSON encoding of the entry with `signature`
empty and HTML escaping off (`Entry.Sign`, `signedPayload`), made with the
entry's `static` key.

| JSON name | Type | Meaning |
|---|---|---|
| `version` | string | Non-empty. A server MAY advertise its build version. |
| `sequence` | uint64 | A client increments it by one for each update. |
| `timestamp` | int64 | Unix nanoseconds. |
| `static` | public key | Identity of the entry. |
| `client` | object | `delegated_servers`: array of server keys. |
| `client_type` | string | Role of the client, for example `route_setup`, `tps`, `resolver`. |
| `server` | object | See below. |
| `signature` | string | Hex signature. |
| `protocol` | string | Multiplexer or carrier hint: `yamux`, `smux` or `quic`. |

`disc.Server`:

| JSON name | Type | Meaning |
|---|---|---|
| `address` | string | TCP `host:port`. REQUIRED. |
| `address_v6` | string | IPv6 `[host]:port`. |
| `address_udp`, `address_udp_v6` | string | QUIC endpoints. Used when `protocol` is `quic`. |
| `address_ws` | string | `ws://` or `wss://` URL. |
| `address_wt` | string | WebTransport `https://` URL. |
| `cert_hash_wt`, `cert_hash_wt_next` | string | Hex SHA-256 of the WebTransport certificate, current and next. |
| `availableSessions` | int | Sessions the server can still accept. |
| `serverType` | string | `official` or `community`. |
| `dht_bootstrap` | bool | The server runs a DHT node on port 100. |

A server whose `address` begins with `skynet://` (`dmsg.SkynetScheme`) is a
relay reached over a skywire route, see [Relays](#relays).

### Stream request and response

`dmsg.StreamRequest` and `dmsg.StreamResponse` (`pkg/dmsg/dmsg/types.go`) are
gob encoded and carried as a `SignedObject`, the signature of the encoding
followed by the encoding. The request is signed by the source key and the
response by the destination key.

| Field | Meaning |
|---|---|
| `Timestamp` (request) | Unix nanoseconds. |
| `SrcAddr`, `DstAddr` (request) | Source and destination `Addr`. Both ports MUST be non-zero. |
| `IPinfo`, `GeoInfo` (request) | Ask the server for the client's public IP, and geolocation. |
| `NoiseMsg` | The Noise handshake message of the stream. |
| `ReqHash` (response) | SHA-256 of the signed request. |
| `Accepted`, `ErrCode` (response) | Result. |
| `IP`, `GeoCountry`, `GeoRegion`, `GeoLat`, `GeoLon` (response) | Set only for an `IPinfo` request. |

`PeerAnnounce` (`Timestamp`, `SrcPK`) is a signed object a server sends on the
first stream of a link to another server.

## Interfaces

### HTTP API

`pkg/dmsg/discovery/api`. Bodies up to 1 MiB (`maxBodyBytes`). A dmsg caller
that exceeds 100 requests per second with a burst of 200 (`rlPerRemoteRPS`,
`rlPerRemoteBurst`) receives 429 with `Retry-After: 1`. Errors are
`{"code": n, "message": "..."}` (`disc.HTTPMessage`). `disc.ErrKeyNotFound`
maps to 404, `disc.ErrBadInput` to 400, `disc.ErrUnauthorized` to 401, an entry
validation error to 422, a body over the limit to 413 and anything else to 500.

| Method and path | Behaviour |
|---|---|
| `GET /dmsg-discovery/entry/{pk}` | 200 `Entry`. 400, 404. |
| `POST /dmsg-discovery/entry/` and `/{pk}` | Body is a signed `Entry`. 200 with the message `wrote a new entry` or `wrote new entry iteration`. See [Registration](#registration). |
| `DELETE /dmsg-discovery/entry` | Body is a signed `Entry`. 200 `deleted entry`. |
| `GET /dmsg-discovery/entries` | 200 array of client keys. |
| `POST /dmsg-discovery/entries/batch` | Body is an array of at most 1000 key strings (`maxBatchEntriesKeys`). 200 object from key to `Entry`, omitting unknown keys. |
| `GET /dmsg-discovery/visorEntries` | 200 visor client entries. |
| `DELETE /dmsg-discovery/deregister` | Deregistration whitelist only, see [02](02-HTTP_Authorization_Middleware.md#deregistration-whitelist). Body an array of keys. 403 for an unlisted key or bad signature. |
| `GET /dmsg-discovery/available_servers` | 200 array of up to 512 server entries. 404 when none. |
| `GET /dmsg-discovery/all_servers` | 200 array of all server entries. 404 when none. |
| `GET /dmsg-discovery/servers/clients` | 200 object from server key to client keys, cached for 15 seconds. |
| `GET /dmsg-discovery/server/{pk}/clients` | 200 array of client keys. |
| `GET /uptimes`, `POST /uptimes` | Visor uptime summaries, as in [04](04-Transport_Discovery.md). |
| `GET /health` | 200 `httputil.HealthCheckResponse` with service name `dmsg-discovery`. |
| `GET /` | Charts page. 404 when charts are not running. |

### CXO Feeds

Layout and cadence are in [31](31-CXO_Feeds.md).

| Port | Identifier | Publisher | Content |
|---|---|---|---|
| 54 | `DmsgDMSGDClientsByServerCXOPort` | discovery | `clients-by-server/<server-pk>` presence leaves and `clients-by-server/servers/<pk>` server entries. |
| 67 | `DmsgDMSGDRegistrationCXOPort` | each client | The client's own signed entry at `entry`. Discovery subscribes. |

### Client configuration

`spec.DmsgConfig` (`pkg/dmsg/dmsgc/spec`) is the `dmsg` object of the visor
configuration. The fields below are per deployment (`spec.Deployment`) and are
also accepted at the top level for a single deployment. The configuration may
also be an array of deployments.

| JSON name | Type | Meaning |
|---|---|---|
| `discovery` | string | Discovery URL. |
| `discovery_dmsg` | string | Discovery `dmsg://<pk>:80` address. |
| `sessions_count` | int | Server sessions to hold. The generated default is 1. |
| `servers` | array of `Entry` | Servers to use without asking discovery. |
| `servers_type` | string | `all`, `official` or `community`. Only the last two filter. The generated default is `all`. |
| `protocol` | string | Multiplexer. The generated default is `yamux`. |
| `carriers` | array of string | Ordered carrier preference from `tcp`, `quic`, `ws`, `wt`. |
| `lan_servers` | array of `Entry` | Servers on the local network. |
| `hypervisor_discovery` | string | A discovery proxy tried before `discovery`. |
| `direct_only` | bool | No discovery, no registration. |
| `server` | object | `DmsgServerConfig`: run an in process server. |
| `relay_max_streams` | int | Streams this visor relays for attached peers. 0 is `DefaultClientMaxRelayedStreams`. Negative refuses. |
| `local_relay` | object | `DmsgLocalRelayConfig`: unix socket or loopback relay acceptor. |

## Behaviour

### Sessions

A session is a byte pipe between a client and a server, a Noise handshake over
it, and a stream multiplexer.

**Handshake.** The client is the initiator of Noise `XK` (`noise.HandshakeXK`)
with secp256k1 Diffie-Hellman, ChaCha20-Poly1305 and SHA-256, and knows the
server key beforehand (`SessionCommon.initClient`, `initServer`). A post
quantum layer is required on top: the initiator sends an ML-KEM-768
encapsulation key in the payload of its first message, the responder returns the
ciphertext, and both mix the shared secret into the transport keys with HKDF
(`pkg/dmsg/noise/pqhybrid.go`, info string `skywire-pq-hybrid-v1`). A handshake
that completes without it is refused. Handshake messages are framed as a 2 byte
big endian length and the message (`noise.WriteRawFrame`). The handshake has a
5 second limit (`dmsg.HandshakeTimeout`).

**Framing.** After the handshake every signed object on a mux stream is sent as
a 2 byte big endian length and `nonce (8 bytes) || ciphertext`
(`SessionCommon.writeObject`). Nonces start at 1 and a receiver rejects a nonce
outside its replay window (`noise.NonceWindow`). The 2 byte header `0x00 0x00`
is a ping: the server echoes it and closes the stream.

**Multiplexing.** Streams run over yamux (`YamuxConfig`: keepalive 30 seconds,
write timeout 45 seconds, stream open timeout 45 seconds) or smux (`SmuxConfig`,
used by a client when the server entry's `protocol` is `smux`). A server session
accepted over TCP, WebSocket or WebTransport uses yamux. A QUIC session
(`protocol` `quic` with `address_udp`) has no Noise layer and no separate
multiplexer. The QUIC stream is the dmsg stream, the connection is TLS protected,
and the connection also carries unreliable datagrams (`WriteDatagram`).

**Carriers.** `Config.Carriers` selects how bytes reach the server. `tcp` dials
`address`, `quic` dials `address_udp`, `ws` dials `address_ws` and `wt` dials
`address_wt`. The first listed carrier that the entry advertises is used, with a
fall back to TCP, or to WebSocket for a failed WebTransport dial. An empty list
selects QUIC when the server advertises it and TCP otherwise. A `skynet://`
address selects the skynet carrier. A dial times out after 10 seconds
(`dmsg.DialTimeout`).

**Liveness.** The client pings each session every 60 seconds by opening a stream
and sending the ping header. Two consecutive failures close the session
(`pingDeadThreshold`). A server serves at most 2048 concurrent streams per
session (`maxConcurrentStreams`).

### Registration

A client publishes `Entry.Client.DelegatedServers`, the servers it holds
sessions to, and refreshes it. The default refresh period is five minutes
(`dmsg.Config.UpdateInterval`). A server refreshes every minute
(`DefaultUpdateInterval`) because its `availableSessions` changes.

Discovery accepts an entry when it has a version, a signature, a static key,
and a `client` or `server` section, when a server `address` is not empty and,
outside test mode, not a loopback address, and when the signature verifies. A
server entry's timestamp MUST be within one minute before and five seconds
after discovery's clock. A client entry's timestamp is not checked. A new key
starts at any sequence; an update MUST have a greater sequence and a timestamp
not earlier than the stored one. Discovery sets `serverType` to `official` when
the server presents the configured passphrase or its key is a listed official
server, and `community` otherwise. Client entries expire after a configured
lifetime unless refreshed.

A client MAY publish its entry over CXO instead, on port 67. Discovery's
aggregator subscribes to each client's feed and applies the same checks to the
`entry` leaf (`API.IngestEntryFromCXO`). The feed key MUST equal `static`. An
entry with the stored sequence refreshes the lifetime and nothing else. While the
feed is healthy the client skips the periodic HTTP update. A sequence gap is
left to the HTTP path.

### Dialing a stream

`Client.DialStream` resolves the destination entry, with a cache that lasts 5
minutes (`entryCacheTTL`) and is used for up to one hour when a lookup fails
(`entryStaleMaxAge`). It then tries, in order, any attached relay sessions, the
server that last carried a stream to the destination, sessions to the
destination's delegated servers, other existing sessions in latency order (the
server forwards to the destination's server), and new sessions to at most two
delegated servers. When no entry exists it tries all connected servers.

The dialer writes a signed `StreamRequest` with a Noise `KK` handshake message
(`noise.HandshakeKK`, the same suite and post quantum layer) on a new mux
stream. The destination answers with a signed `StreamResponse` carrying its
handshake message. The dialer verifies `ReqHash` and the signature against the
destination key. After this the stream is a `noise.ReadWriter`, with frames of
at most 4096 bytes: a 2 byte length, then the encrypted payload with an 8 byte
nonce and a 16 byte tag. A stream idle for two minutes is closed
(`StreamIdleTimeout`).

### Server relaying

On a request, the server verifies the request signature, then looks for a local
session of `DstAddr.PK`. It forwards the original signed request on a new stream
of that session, verifies the response against the request, writes the response
back, and copies bytes in both directions. A bridge closes after 5 minutes
without data in either direction. A request that came from a peer server is
served only from the local sessions and is not forwarded again. A request whose source key differs from the session key is a
relayed request. The server accepts it only from a peer or when
`ServerConfig.AcceptRelayedRequests` is set (default true), and counts it
against the relay stream limit.

An `IPinfo` request for the server itself is answered with the TCP source
address of the session.

### Server peering

Servers are peers of each other. A server dials the servers in
`ServerConfig.Peers` and the servers it finds in discovery every
`UpdateInterval`, retrying with a back off that grows from 5 seconds toward one
minute. On each link it sends a signed `PeerAnnounce`. The receiver accepts it
when the signature verifies against the session key, the announced key is
allowed (`ServerConfig.AcceptedPeerPKs`, empty allows any) and fewer than
`MaxPeerLinks` (512, `DefaultMaxPeerLinks`) peer links exist. A peer link
carries a request for a client that is not on the receiving server. A server
tries its peers in turn and remembers destinations that every peer reported
missing.

| Value | Identifier |
|---|---|
| 2048 | `DefaultMaxSessions` |
| 512 | `DefaultMaxPeerLinks` |
| 4096 | `DefaultMaxRelayedStreams` |

### Relays

A visor can carry the stream requests of another client over its own server
sessions. The attached peer holds one session to the visor, with no discovery
entry. The session is the same Noise `XK` and yamux session, and its Noise
handshake proves the peer's key. The visor forwards each request unchanged, so
the destination sees the peer's key.

- **Skynet relay.** The visor listens on `DmsgRelayPort` (70) over skynet. A
  peer's server entry carries `skynet://<visor-pk>:70` as its address and the
  peer dials it through `Config.SessionDialer`. `Client.AcceptRelaySession`
  accepts the session after the whitelist check.
- **Local relay.** `DmsgLocalRelayConfig` opens the same acceptor on a unix socket
  (default `<local_path>/dmsg_relay.sock`, mode 0600) and optionally a loopback TCP
  address. The attaching process sends a hello that names the acceptor key and
  then runs the session handshake. `allowed_keys` restricts the peers and is
  REQUIRED for the TCP address.

Relayed streams beyond `relay_max_streams` are refused.

### Discovery feeds

Discovery publishes `clients-by-server/<server-pk>` (an empty leaf, version 2,
present while the server has delegated clients) and
`clients-by-server/servers/<pk>`, the signed entry of each registered server as
a version 1 framed gzip JSON leaf (`serverfeed.LeafPath`). A client and the
servers use the second to list servers without an HTTP call.

## Security Considerations

A session authenticates both ends by key. The stream handshake authenticates the
two clients to each other, so a server and a relay see only ciphertext and the
signed request envelope: both keys and ports. The post quantum layer is
mandatory, so a peer that cannot negotiate it cannot connect. Discovery verifies
each entry against its own key and orders updates by sequence; it does not
authenticate that a server address is reachable. Servers are untrusted for
confidentiality. A dmsg caller of an HTTP service is identified by its key, see
[02](02-HTTP_Authorization_Middleware.md).

## References

- [01 Overview](01-Overview.md)
- [31 CXO Feeds](31-CXO_Feeds.md)
- [DMSG transport](../transports/DMSG.md)
- Noise Protocol Framework, revision 34, patterns `XK` and `KK`
