# Overview

| Status   | Implemented                                                   |
|----------|---------------------------------------------------------------|
| Revision | 2026-10-27, skywire 38152beed                                 |
| Code     | `pkg/skyenv`, `deployment`, `pkg/visor`, `pkg/router`, `pkg/transport` |

## Abstract

Skywire is a mesh network of visors, processes identified by secp256k1 public
keys, that exchange encrypted traffic over transports and routes. A small set
of deployment services bootstraps the network. This document names the
components and the dmsg port registry. Each component has its own
specification.

## Overview

### Node types

- **Visor.** The network participant. It runs applications, holds transports
  and forwards packets. A visor MAY embed a Route Setup Node and a Transport
  Setup Node.
- **Route Setup Node (RSN).** Builds multi-hop routes by reserving route IDs
  and installing routing rules on each visor of the path. See
  [13](13-Setup_Node.md), [09](09-Route_Setup_Process.md) and
  [28](28-Cascading_Route_Setup.md).
- **Transport Setup Node (TPS).** Creates, lists and removes transports on a
  visor on behalf of an authorised caller. See
  [30](30-Transport_Setup_Node.md).
- **Dmsg server.** Relays dmsg sessions between clients that cannot connect
  directly. See [05](05-Messaging_System.md).

### Transports

A transport is a bidirectional encrypted link between two visors, identified by
a UUID. The type is one of the `tptypes.Type` values in
`pkg/transport/types`.

| Type     | Meaning                                                     |
|----------|-------------------------------------------------------------|
| `stcpr`  | TCP, peer address resolved by the address resolver          |
| `sudph`  | UDP with hole punching, address resolved by the address resolver |
| `stcp`   | TCP, peer address from a locally configured PK table        |
| `dmsg`   | carried over dmsg                                           |
| `squicr` | QUIC, peer address resolved by the address resolver         |
| `swsr`   | WebSocket between two visors                                |
| `swtr`   | WebTransport between two visors                             |
| `webrtc` | WebRTC data channel                                         |

The legacy names `quic`, `squic` and `ws` are accepted on input
(`tptypes.QUICLegacy`, `QUICLegacy2`, `WSLegacy`). A transport entry carries a
`tptypes.Label`: `user`, `automatic`, `skycoin`, `setup` or `sysfeed`
(`pkg/transport/entry.go`). See [03](03-Transport.md) and
[07](07-Transport_Management.md).

### Routes

A route is a unidirectional path over one or more transports. Each hop has a
routing rule in the visor's routing table. The rule types are `RuleReverse`
(the packet is consumed locally), `RuleForward` (the first hop, toward a
remote visor) and `RuleIntermediary` (a middle hop) in `pkg/routing/rule.go`.
A forward and a reverse route form a route group. After the rules are
installed, the two edge visors run a Noise handshake over the route, so
intermediary visors see only ciphertext. See [06](06-Packets.md),
[10](10-Routing_Table.md) and [11](11-Router.md).

Every packet starts with a header of `routing.PacketHeaderSize` (7) bytes: the
type, a 4 byte route ID and a 2 byte payload size.

## Addressing

Deployment services are served over dmsg HTTP at `dmsg://<service-pk>:80`.
They are not reachable over plain HTTP on the internet. The public keys come
from `deployment/services-config.json`, embedded as `deployment.Prod`
(`deployment.Services`). Visors fetch the same document from the
configuration bootstrapper ([23](23-Config_Bootstrapper.md)).

| Service | `services-config.json` key | Specification |
|---|---|---|
| Configuration bootstrapper | `conf_dmsg` | [23](23-Config_Bootstrapper.md) |
| Dmsg discovery | `dmsg_discovery_dmsg` | [05](05-Messaging_System.md) |
| Transport discovery | `transport_discovery_dmsg` | [04](04-Transport_Discovery.md) |
| Address resolver | `address_resolver_dmsg` | [20](20-Address_Resolver.md) |
| Route finder | `route_finder_dmsg` | [08](08-Route_Finder.md) |
| Service discovery | `service_discovery_dmsg` | [21](21-Service_Discovery.md) |
| Reward system | `reward_system_dmsg` | dmsg HTTP web service, not specified here |
| Route setup nodes | `route_setup_nodes` (public keys) | [13](13-Setup_Node.md) |
| Transport setup nodes | `transport_setup` (public keys) | [30](30-Transport_Setup_Node.md) |
| STUN servers | `stun_servers` (UDP `host:port`, not dmsg) | `pkg/deployment/stun` |
| Network monitor | none | [22](22-Network_Monitor.md) |
| Transport graph | an HTML page served by transport discovery | [04](04-Transport_Discovery.md) |

`deployment/services-config-clearnet.json` is a separate document and is not
the default. The deployment services publish their data as CXO feeds, see
[31](31-CXO_Feeds.md).

## Interfaces

### Dmsg port registry

Constants in `pkg/skyenv/skyenv.go`. Dmsg HTTP services listen on port 80.

| Port | Identifier | Purpose |
|---|---|---|
| 7 | `DmsgCtrlPort` | dmsgctrl echo protocol |
| 8 | `DmsgPingPort` | dmsg ping |
| 22 | `DmsgPtyPort` | dmsgpty pseudoterminal |
| 36 | `DmsgSetupPort` | route setup node listener |
| 46 | `DmsgHypervisorPort` | hypervisor RPC, visor dials hypervisor |
| 47 | `DmsgTransportSetupPort` | transport setup RPC on a visor |
| 48 | `DmsgTransportSetupServicePort` | transport setup service requests |
| 49 | `DmsgGRPCPort` | gRPC for remote stats |
| 50 | `DmsgCXOPort` | visor telemetry CXO feed |
| 51 | `DmsgTPDMetricsCXOPort` | TPD transport metrics feed |
| 52 | `DmsgTPDUptimeCXOPort` | TPD visor uptime feed |
| 53 | `DmsgSDServicesCXOPort` | service discovery services feed |
| 54 | `DmsgDMSGDClientsByServerCXOPort` | dmsg discovery clients-by-server feed |
| 55 | `DmsgTPDAllTransportsCXOPort` | TPD all-transports snapshot feed |
| 56 | `DmsgWebRTCSignalPort` | WebRTC signalling |
| 62 | `SkychatVoiceSignalPort` | skychat voice call signalling |
| 63 | `SkychatVoiceMediaPort` | skychat voice media stream |
| 64 | `SkychatFilePort` | skychat file transfer |
| 65 | `DmsgVisorRPCPort` | visor RPC for remote CLI |
| 67 | `DmsgDMSGDRegistrationCXOPort` | dmsg discovery registration feed |
| 68 | `DmsgTransportQueryPort` | RSN signed transport list queries |
| 69 | `DmsgVisorTPListCXOPort` | visor transport list feed |
| 70 | `DmsgRelayPort` | dmsg relay on a visor, served over skynet |
| 71 | `DmsgVisorARBindCXOPort` | visor address bindings feed |
| 72 | `DmsgVisorSDRegCXOPort` | visor service registrations feed |
| 73 | `DmsgTPDStatsCXOPort` | TPD aggregate statistics feed |
| 74 | `DmsgARBindingsCXOPort` | address resolver bindings feed |
| 75 | `DmsgTPDRoutingCXOPort` | TPD routing feed |
| 76 | `DmsgARReachCXOPort` | address resolver reachability feed |
| 77 | `DmsgTPDVisorBWCXOPort` | TPD per-visor bandwidth feed |
| 78 | `DmsgConfCXOPort` | configuration bootstrapper services feed |
| 79 | `DmsgTPDPerKeyCXOPort` | TPD per-key transport counts feed |
| 100 | `DmsgDHTPort` | Kademlia DHT |
| 136 | `DmsgAwaitSetupPort` | visor listener for route setup |
| 8060 | `SkychatGroupProbePort` | skychat group describe requests |

`TransportPort` (45) is the transport listening port, not a dmsg port.

## Behaviour

A transport sends a `TransportPingPacket` every `transportPingInterval` (60
seconds, `pkg/transport/managed_transport.go`) with route ID 0. The packet is
handled at the transport layer and needs no route.

## References

- [CONVENTIONS.md](../CONVENTIONS.md)
