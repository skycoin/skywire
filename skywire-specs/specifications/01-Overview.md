# Overview

Skywire is a decentralized, privacy-focused mesh network. The network is composed of *Visors* — processes identified by secp256k1 public keys that communicate over encrypted channels.

## Node Types

- **Visor** — the primary network participant. Runs applications, establishes transports, and routes packets. Each visor has a unique public key. A visor may optionally run an embedded *Route Setup Node* and/or *Transport Setup Node*.

- **Route Setup Node (RSN)** — coordinates the establishment of multi-hop routes between visors. The RSN connects to each visor along a route path via DMSG port 136, reserves route IDs, generates routing rules, and distributes them. RSNs can run as standalone deployment services or embedded within a visor.

- **Transport Setup Node (TPS)** — remotely manages transports on visors. Can create, list, and remove transports on behalf of network operators. Connects to visors via DMSG port 47.

- **DMSG Server** — relay server in the Distributed Messaging System. Bridges DMSG client sessions for visors that cannot communicate directly. Identified by public key and TCP address.

## Core Concepts

### Transports

A *Transport* is a bidirectional, encrypted line of communication between two visors (called *Transport Edges*). Each transport has a unique *Transport ID* (UUID) and a *Transport Type* that identifies the underlying protocol.

Transport types:
- **STCPR** — TCP with address resolution via the Address Resolver service
- **SUDPH** — UDP with NAT hole-punching via the Address Resolver service
- **STCP** — TCP with locally configured PK-to-IP mapping (no external service)
- **DMSG** — communication relayed through DMSG servers

Transports carry *Transport Labels* (`skycoin`, `automatic`, `user`) that identify their origin and control access via the Transport Setup Node.

### Routes

A *Route* is a unidirectional path through one or more transports. Each hop in a route is governed by a *Routing Rule* stored in the visor's *Routing Table*. A bidirectional connection between two visors consists of a forward route and a reverse route, forming a *Route Group*.

Routing rule types:
- **Forward** — the initiating edge's rule; specifies the next route ID and transport
- **Consume** — the responding edge's rule; delivers the packet to the local application
- **IntermediaryForward** — an intermediary visor's rule; forwards the packet to the next hop

### Packets

Packets are the data units transmitted over routes. Each packet has a 7-byte header:

```
| type (1 byte) | route ID (4 bytes) | payload size (2 bytes) | payload |
```

Packet types:
- `DataPacket` (0) — application data
- `ClosePacket` (1) — connection close
- `KeepAlivePacket` (2) — route keepalive
- `HandshakePacket` (3) — Noise protocol handshake + capability negotiation
- `PingPacket` (4) — route-level latency measurement
- `PongPacket` (5) — route-level pong response
- `ErrorPacket` (6) — error notification
- `SACKPacket` (7) — selective acknowledgment (for route multiplexing)
- `TransportPingPacket` (8) — transport-level latency measurement (no route required)
- `TransportPongPacket` (9) — transport-level pong response

### Route Groups and Noise Encryption

A *Route Group* wraps the forward and reverse routes into a bidirectional connection. After routing rules are established, the two edge visors perform a Noise protocol handshake (ChaCha20-Poly1305) over the route. Intermediary visors see only encrypted payload — they know only the previous and next hop, not the source or destination.

Route groups support capability negotiation via the handshake:
- **CapMux** — route multiplexing with sequenced DataPackets
- **CapSACK** — selective acknowledgment retransmission

## Deployment Services

Centralized services that bootstrap and support the network:

| Service | Purpose | HTTP Endpoint | DMSG Port |
|---|---|---|---|
| DMSG Discovery | Maps visor PKs to DMSG server sessions | dmsgd.skywire.skycoin.com | 80 |
| Transport Discovery | Registers and queries transports | tpd.skywire.skycoin.com | 80 |
| Address Resolver | Maps visor PKs to IP addresses for direct transports | ar.skywire.skycoin.com | 80 |
| Route Finder | Computes multi-hop route paths from the transport graph | rf.skywire.skycoin.com | 80 |
| Service Discovery | Registers VPN, proxy, and other application services | sd.skycoin.com | 80 |
| Uptime Tracker | Tracks visor online status for reward eligibility | ut.skywire.skycoin.com | 80 |
| Config Bootstrapper | Provides initial visor configuration | conf.skywire.skycoin.com | — |

## DMSG Port Assignments

| Port | Service |
|---|---|
| 7 | DMSG Ctrl (echo protocol) |
| 8 | DMSG Ping |
| 22 | DMSG Pty (pseudoterminal) |
| 36 | Route Setup Node (standalone RSN listener) |
| 46 | Hypervisor RPC |
| 47 | Transport Setup RPC |
| 48 | Transport Setup Service |
| 49 | gRPC (remote monitoring) |
| 80 | DMSG HTTP (health, log server, services) |
| 136 | Route Setup Await (visor listener for RSN connections) |

## Transport-Level Latency Measurement

Transports measure their own latency using `TransportPingPacket` (type 8) and `TransportPongPacket` (type 9) with route ID 0. These frames are intercepted at the transport layer before reaching the router, requiring no route setup or RSN involvement. Pings are sent every 30 seconds; the first ping fires immediately when the transport is established.
