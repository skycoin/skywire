# DMSG Transport

| Status   | Implemented                                    |
|----------|------------------------------------------------|
| Revision | 2026-10-27, skywire 38152beed                  |
| Code     | `pkg/transport/network/dmsg.go`, `pkg/dmsg`    |

## Abstract

The `dmsg` transport type (`tptypes.DMSG`) carries a skywire transport over a
dmsg stream. It needs no direct route between the two visors. The streams run
through dmsg servers, so both visors need a path to each other through the dmsg
network. The dmsg protocol is specified in
[05-Messaging_System.md](../specifications/05-Messaging_System.md).

## Overview

`dmsgClientAdapter` wraps the visor's `dmsg.Client` as a transport `Client`.
Unlike `stcpr` and `sudph`, there is no address resolver step. The peer is
reached by its public key through the dmsg client's dial procedure, which uses
discovery, existing server sessions, server to server forwarding and relays.

## Interfaces

| Method | Behaviour |
|---|---|
| `Dial(ctx, remote, port)` | `dmsg.Client.DialStream` to `remote:port`. |
| `Listen(port)` | `dmsg.Client.Listen(port)`. |
| `Start` | Does nothing. The wrapped client is already serving. |
| `Close` | Does nothing. The client is shared with other components and closed by its owner. |
| `PK`, `SK` | The dmsg client's keys. |

`dmsgTransportAdapter` wraps a `*dmsg.Stream`. It exposes `LocalPK`,
`RemotePK`, `LocalPort`, `RemotePort` and `Network` (`dmsg`). Its `ConnDetails`
reports the key of the dmsg server that carries the stream and no remote IP.

## Data Formats

The `dmsg` object of the visor configuration is `spec.DmsgConfig`
(`pkg/dmsg/dmsgc/spec`). The full field list is in
[05-Messaging_System.md](../specifications/05-Messaging_System.md#client-configuration).
A generated configuration sets the following.

```json
{
    "dmsg": {
        "discovery": "dmsg://<dmsg-discovery-pk>:80",
        "sessions_count": 1,
        "servers": [],
        "servers_type": "all",
        "protocol": "yamux"
    }
}
```

| Field | Type | Meaning |
|---|---|---|
| `discovery` | string | Discovery address. `discovery_dmsg` holds the `dmsg://` form separately. |
| `sessions_count` | integer | Server sessions to hold. |
| `servers` | array of `disc.Entry` | Server entries to use without a discovery lookup. Each entry has `static` (the server key) and `server` (`address`, and optionally `address_ws`, `address_udp` and the other fields of `disc.Server`). |
| `servers_type` | string | `all`, `official` or `community`. `official` and `community` restrict the sessions to servers whose `serverType` matches. |
| `protocol` | string | Multiplexer, `yamux` by default. |
| `carriers` | array of string | Ordered preference from `tcp`, `quic`, `ws`, `wt`. |

## Behaviour

A dmsg stream is end to end encrypted between the two visors, so the server that
relays it sees only ciphertext. A stream between visors on different servers is
forwarded by one server to a peer server. A visor that holds no server sessions
of its own can attach to another visor's dmsg relay and have its streams
carried over that visor's sessions (see Relays in 05).

## References

- [05 Dmsg](../specifications/05-Messaging_System.md)
- [STCPR](STCPR.md), [SUDPH](SUDPH.md), [STCP](STCP.md)
