# Transport Setup Node

| Status   | Implemented                                                                 |
|----------|-----------------------------------------------------------------------------|
| Revision | 2026-10-27, skywire f37a464a4 |
| Code     | `pkg/deployment/tps`, `pkg/services/tps`, `pkg/transport/setup`, `pkg/visor/embedded_tps.go`, `pkg/visor/api_tps.go` |

## Abstract

A transport setup node (TPS) creates and lists transports on a visor on behalf
of an operator. A caller asks the TPS to set up a transport between two visors.
The TPS asks the visor to create it. The visor accepts requests only from TPS
keys it trusts.

## Overview

Two services take part. The TPS is a dmsg client that serves a request interface
on port 48. A visor serves the transport gateway on port 47 and refuses callers
that are not in its trusted TPS list. A visor MAY also embed a TPS. The TPS
holds no transports. It forwards each request to the target visor.

## Addressing

| Port | Identifier | Listener | Purpose |
|---|---|---|---|
| 48 | `skyenv.DmsgTransportSetupServicePort` | the TPS | Requests to the TPS. |
| 47 | `skyenv.DmsgTransportSetupPort` | every visor | Requests from a TPS to the visor. |
| 80 | | the standalone TPS | `GET /health` over dmsg HTTP. |

The TPS keys are `transport_setup` in `deployment.Services`. They are copied to
`transport.transport_setup` in the visor configuration, with the visor's own
additions in `transport.user_transport_setup`. A standalone TPS reads a
configuration file (`config.Config`) with `public_key`, `secret_key`, `port` and
`dmsg`. A visor embeds a TPS when `transport.tps_sk` is set, and the embedded TPS
uses that key with its own dmsg client.

## Data Formats

The RPC types use gob encoding. The same wire type is used by the standalone and
the embedded TPS.

`TransportSetupRequest` (`pkg/deployment/tps/api`):

| Field | Type | Meaning |
|---|---|---|
| `TargetPK` | public key | The visor that creates the transport. |
| `RemotePK` | public key | The other edge. |
| `Type` | string | Transport type from [01](01-Overview.md). |

`TransportSetupResponse`: `ID` (UUID), `Local`, `Remote` (public keys) and `Type`.
`GetTransportsRequest` has `TargetPK`, and `GetTransportsResponse` has
`Transports` (array of `TransportSetupResponse`). `RemoveTransportRequest` has
`TargetPK` and `ID` (string).

The visor side types (`pkg/transport/setup`) are `TransportRequest` (`RemotePK`,
`Type`), `UUIDRequest` (`ID`), `TransportResponse` (`ID`, `Local`, `Remote`,
`Type`) and `BoolResponse` (`Result`).

## Interfaces

### RPC

#### Port 48, service `SetupRPCGateway`

The TPS registers `SetupRPCGateway` on each accepted stream. It does not check
the caller key.

| Method | Request | Reply | Behaviour |
|---|---|---|---|
| `HealthCheck` | `HealthCheckArgs{}` | `HealthCheckReply{Status}` | `Status` is `OK`. |
| `AddTransport` | `TransportSetupRequest` | `TransportSetupResponse` | Dials `TargetPK` on port 47 and calls `TransportGateway.AddTransport`. Fails when `TargetPK` equals `RemotePK`. |
| `GetTransports` | `GetTransportsRequest` | `GetTransportsResponse` | Calls `TransportGateway.GetTransports` on the target. |
| `RemoveTransport` | `RemoveTransportRequest` | none | Always returns `ErrRemoteRemovalNotAllowed`. |

The embedded TPS has the same four methods (`TPSRPCGateway`). It bounds
`AddTransport` and `GetTransports` at 30 seconds. Its `RemoveTransport` also
refuses remote callers and is available only to the visor itself.

#### Port 47, service `TransportGateway`

A visor serves it on its dmsg client (`transport.TransportListener`). A stream
from a key outside the visor's trusted TPS list is closed.

| Method | Request | Reply | Behaviour |
|---|---|---|---|
| `AddTransport` | `TransportRequest` | `TransportResponse` | Saves a transport to `RemotePK` with the label `skycoin` (`transport.LabelSkycoin`). |
| `RemoveTransport` | `UUIDRequest` | `BoolResponse` | Removes the transport when its label is `skycoin` or `automatic`. Otherwise `ErrIncorrectType`. |
| `GetTransports` | none | array of `TransportResponse` | Transports labelled `skycoin`, `automatic` or `user`. |

### HTTP API

The standalone TPS serves an administration API on `:<port>` when the
configuration `port` is not zero. It has no authentication. Bodies are JSON with
unknown fields rejected and required fields validated. The server sets a header
read timeout of 2 seconds and an idle timeout of 30 seconds.

| Method and path | Request | Response |
|---|---|---|
| `POST /add` | `{"from": pk, "to": pk, "type": string}` | 200 `setup.TransportResponse`. 400 for a bad body or equal keys. 500 when the call to the visor fails. |
| `POST /remove` | `{"from": pk, "id": uuid}` | 200 `setup.BoolResponse`. 400. 500. |
| `GET /{pk}/transports` | | 200 array of `setup.TransportResponse`. 400. 500. |

Error bodies are `{"error": "<text>"}`. A call to a visor that does not trust the
TPS fails with the text "remote connection closed (visor may not trust current
pkey)". Removal through `POST /remove` calls `TransportGateway.RemoveTransport`
directly with the label rule above.

## Behaviour

A visor creates the transport as for a local `tp add`, with the label `skycoin`.
The label marks a transport set up by an operator. Transports labelled
`automatic` come from the visor's public autoconnect and `user` from the visor's
owner. A transport labelled `setup` is a control link to a route setup node and
is not a TPS artefact; the route finder leaves it out of data routes
([08](08-Route_Finder.md)). The visor lists `user` transports to the TPS but does
not let it remove them.

The visor API probes a TPS with `SetupRPCGateway.HealthCheck` on port 48
(`Visor.TPSExternalHealthCheck`) and calls `AddTransport` and `GetTransports` on a
TPS it names (`TPSExternalAddTransport`, `TPSExternalGetTransports`), each under a
30 second limit. The visor orders its configured TPS keys by health and latency
(`GetTransportSetupNodesSorted`).

## Security Considerations

The authorization is on the visor. The visor accepts a setup request only from a
key in `transport.transport_setup` or `transport.user_transport_setup`. The TPS
does not authenticate the callers of its dmsg service or its HTTP API, so any
caller that reaches either can ask a TPS to create a transport on any visor that
trusts it. The TPS dmsg service does not remove transports, and a TPS cannot
remove a transport that has the label `user`.

## References

- [01 Overview](01-Overview.md)
- [03 Transport](03-Transport.md)
- [07 Transport Management](07-Transport_Management.md)
