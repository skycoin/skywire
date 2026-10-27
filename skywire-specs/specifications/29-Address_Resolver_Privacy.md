# Address Resolver Privacy

| Status   | Implemented                                                                         |
|----------|-------------------------------------------------------------------------------------|
| Revision | 2026-10-27, skywire 38152beed                                                       |
| Code     | `pkg/visor/visorconfig`, `pkg/transport`, `pkg/visor/reach_card.go`, `pkg/visor/init_transport.go` |

## Abstract

A visor setting decides whether the visor makes its network address available
through the Address Resolver. A visor that opts out can still dial other visors
and use dmsg, but peers cannot look up its address.

## Overview

The Address Resolver stores a visor's address so that other visors can open
direct transports to it (see Address Resolver). The setting `ar_transport_limit`
in the `transport` section of the visor configuration is the switch.

## Data Formats

`visorconfig.Transport.ARTransportLimit`, JSON key `ar_transport_limit`, an
integer that defaults to 0. `transport.ManagerConfig.ARTransportLimit` carries
the value to the transport manager. Only the sign is used.

| Value | Meaning                                                    |
|-------|------------------------------------------------------------|
| >= 0  | the visor registers with the Address Resolver              |
| < 0   | the visor does not register; peers cannot resolve it       |

A positive value is not a count. A visor does not deregister after a number of
transports.

The configuration generator exposes the value as the flag `ar-transport-limit`.
The route setup node (`pkg/services/sn`) sets the value to -1.

## Behaviour

`Manager.ShouldRegisterAR` returns true when the value is 0 or greater.

A visor with a negative value also serves no reach card. `Visor.advertisesReach`
is false, and `GET /reach` answers 404 (see Address Resolver).

A visor that does not register can still create outbound transports, use dmsg
and use transports that are already established. Peers cannot open `stcpr`,
`sudph`, `squicr` or `swtr` transports to it, because they have no address to
dial.

A public visor (`public_autoconnect`) needs registration so that peers can reach
it. The configuration permits the combination with a negative value.

Registration is separate from load control for public visors, which is based on
the number of distinct peer visors (see Public Visors).

## Security Considerations

A negative value withholds the visor's reach card. It does not hide the address
from peers that already hold a transport to it.

## References

- Address Resolver
- Public Visors
