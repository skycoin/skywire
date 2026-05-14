# Glossary

**Visor:** A Skywire network node identified by a secp256k1 public key. Runs applications, establishes transports, and routes packets.

**Transport:** A bidirectional, encrypted line of communication between two visors (transport edges). Identified by a UUID (Transport ID) and a type (STCPR, SUDPH, STCP, DMSG).

**Transport Edge:** One of the two visors connected by a transport.

**Transport ID:** A UUID that uniquely identifies a transport. Derived deterministically from the two edge PKs and the transport type.

**Transport Type:** The underlying protocol implementation: STCPR (TCP with address resolution), SUDPH (UDP hole-punch), STCP (TCP with local PK table), DMSG (relayed through DMSG server).

**Transport Label:** Metadata indicating the origin of a transport: `skycoin` (created by TPS), `automatic` (created by autoconnect), `user` (created manually).

**Route:** A unidirectional path through one or more transports. Each hop is governed by a routing rule.

**Route Group:** A pair of forward and reverse routes forming a bidirectional connection between two visors. Wrapped in Noise encryption after the handshake.

**Routing Rule:** An entry in the routing table that tells the router how to handle a packet: Forward (next hop), Consume (deliver locally), or IntermediaryForward (blind forward).

**Route ID:** A uint32 value identifying a routing rule within a visor's routing table. Allocated by the visor on request from the RSN.

**Route Setup Node (RSN):** A trusted node that coordinates multi-hop route establishment by connecting to each visor along the path, reserving route IDs, and distributing routing rules.

**Transport Setup Node (TPS):** A node that remotely manages transports on visors — can create, list, and remove transports with `skycoin` or `automatic` labels.

**DMSG (Distributed Messaging System):** A relay-based communication layer. DMSG clients connect to DMSG servers and communicate via multiplexed, Noise-encrypted streams.

**DMSG Server:** A relay that bridges DMSG client sessions. Identified by PK and TCP address. Registered in DMSG Discovery.

**DMSG Discovery:** A centralized service mapping visor PKs to their DMSG server connections (delegated servers).

**Transport Discovery (TPD):** A centralized service that registers and queries transport entries. Provides bandwidth metrics, uptime tracking, and version statistics.

**Address Resolver (AR):** A service mapping visor PKs to IP addresses for direct transport establishment (STCPR, SUDPH).

**Route Finder (RF):** A service that computes multi-hop route paths from the transport graph using BFS.

**Service Discovery (SD):** A registry for Skywire application services (VPN, proxy, visor) with geo-location and version data.

**Uptime Tracker (UT):** A service tracking visor online status for reward eligibility.

**Packet:** A data unit transmitted over routes. 7-byte header (type, route ID, payload size) followed by variable-length payload.

**Noise Protocol:** ChaCha20-Poly1305 authenticated encryption used for route group encryption. Handshake pattern: XX for routes.

**Skynet Port Forwarding:** Exposing local TCP ports over the Skywire network via skynet and/or DMSG transports. Configured per-port with metadata (label, description, whitelist).

**Resolving Proxy:** An embedded SOCKS5 proxy that resolves `.dmsg` or `.skynet` domain suffixes by routing requests through the visor's DMSG or skynet connections.

**Porter:** The ephemeral port allocator used by the DMSG client for stream management. Ports 49152-65535 are allocated dynamically; well-known ports (listeners) are reserved explicitly.

**Circuit Breaker:** Per-destination failure tracking on the RSN. Opens after consecutive failures to fast-fail subsequent requests, preventing wasted dial attempts to known-bad visors.
