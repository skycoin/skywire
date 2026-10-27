# STUN Server

| Status   | Implemented                                                                  |
|----------|------------------------------------------------------------------------------|
| Revision | 2026-10-27, skywire f37a464a4 |
| Code     | `pkg/deployment/stun`, `pkg/services/stun`, `pkg/transport/network/stun_client.go`, `pkg/visor/init_transport.go` |

## Abstract

The STUN server answers Binding Requests over UDP. It supports the NAT type
discovery of RFC 3489, which needs the response to come from a different IP
address or port than the request reached. Visors use it to learn their public
address and NAT class.

## Overview

A visor sends Binding Requests to the STUN servers in its configuration. The
result decides whether the visor creates `sudph` transports and is published as
the visor's NAT class in the reach declaration (see Address Resolver). The STUN
server is not an HTTP service. It is the one deployment service that is reached
over UDP rather than dmsg HTTP, and it holds no state.

## Addressing

Each STUN server is a `host:port` string. The visor takes the list from the
`stun_servers` key of its configuration. The Config Bootstrapper serves the
deployment's list under the same key (see Config Bootstrapper). The server
listens on IPv4 only.

## Interfaces

### Sockets

The server binds four UDP sockets (`stunserver.Server`).

| Socket | IP                          | Port                  |
|--------|-----------------------------|-----------------------|
| 1      | primary                     | primary, 3478 default |
| 2      | primary                     | alternate, 3479 default |
| 3      | secondary                   | primary               |
| 4      | secondary                   | alternate             |

Both IP addresses are required (`primary_ip` and `secondary_ip` in
`stun.Config`). The service type name is `stun-server`.

### Messages

The server reads datagrams of up to 1024 bytes. It parses the 20 byte STUN
header and the attributes. It ignores a datagram that is shorter than the header,
declares a length that exceeds the data, or is not a Binding Request (type
`0x0001`).

A Binding Request may carry `CHANGE-REQUEST` (`0x0003`). The server answers from
the socket selected by the flags in the last byte of the value.

| Flag      | Value | Effect on the source of the response            |
|-----------|-------|--------------------------------------------------|
| change IP | `0x04`| the other IP, same port as the receiving socket  |
| change port | `0x02` | the same IP, the other port                    |
| both      |       | the other IP and the other port                  |
| none      |       | the receiving socket                             |

The response is a Binding Response (`0x0101`) sent to the source address of the
request. It copies the transaction ID of the request and carries these attributes
in this order.

| Attribute                         | Code     | Value                                   |
|-----------------------------------|----------|-----------------------------------------|
| `XOR-MAPPED-ADDRESS`              | `0x0020` | the client address, XOR coded           |
| `MAPPED-ADDRESS`                  | `0x0001` | the client address                      |
| `SOURCE-ADDRESS`                  | `0x0004` | the address the response is sent from   |
| `RESPONSE-ORIGIN`                 | `0x802b` | the same address                        |
| `CHANGED-ADDRESS`                 | `0x0005` | the other IP and the other port of the receiving socket |
| `OTHER-ADDRESS`                   | `0x802c` | the same address                        |
| `SOFTWARE`                        | `0x8022` | `skywire-stun`                          |
| `FINGERPRINT`                     | `0x8028` | CRC-32 of the message XOR `0x5354554e`  |

An address attribute is 8 bytes: a zero byte, the family `0x01` and the port and
IPv4 address. The server has no authentication, and no other message types.

## Behaviour

### Visor use

A visor runs the NAT discovery of the go-stun client against its configured
servers in order (`network.GetStunDetails`). The first server that returns a
result other than an error, unknown or blocked ends the search. The result is the
NAT type and the public address. If every server fails, the last result is kept.

The visor does not probe on the js platform.

If the result is an error, unknown or blocked, the visor repeats the probe every 5
minutes (`stunRetryInterval`), and starts its `sudph` client when a probe
succeeds. A symmetric NAT or a symmetric UDP firewall is a final result and is not
repeated.

When it dials a peer, a visor tries `sudph` only when the NAT type is none, full cone,
restricted or port restricted. It waits up to 20 s for the first probe to finish
before it decides.

The public address is also a source for the `public_ip` it declares to the
Address Resolver. The visor takes that address from the dmsg servers first and uses
STUN as the fallback (`resolvePublicIPForAR`).

The reach declaration carries the NAT class (`natClass`).

| `stun.NATType`            | Class `arfeed.NAT*`  |
|---------------------------|----------------------|
| `NATNone`                 | `open`               |
| `NATFull`                 | `full-cone`          |
| `NATRestricted`           | `restricted`         |
| `NATPortRestricted`       | `port-restricted`    |
| `NATSymmetric`            | `symmetric`          |
| `NATSymmetricUDPFirewall` | `udp-firewall`       |

Other results give an empty class.

## Security Considerations

The server answers any datagram to any address with a response that is about the
size of the request, and keeps no state. A visor trusts the reported address and
NAT type of the first server that answers.

## References

- Address Resolver
- Config Bootstrapper
- Public Autoconnect
