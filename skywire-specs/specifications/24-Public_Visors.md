# Public Visors

| Status   | Implemented                                                                   |
|----------|-------------------------------------------------------------------------------|
| Revision | 2026-10-27, skywire f37a464a4 |
| Code     | `pkg/visor/init_transport.go`, `pkg/app/appdisc`, `pkg/visor/visorconfig`     |

## Abstract

A public visor advertises itself in Service Discovery as a visor that accepts
incoming `stcpr` transports. Other visors find it there and connect to it. The
visor validates that it is reachable, and it stops advertising itself while it
serves too many distinct peers.

## Overview

Visors with `public_autoconnect` enabled read the `visor` entries of Service
Discovery and open transports to them (see Public Autoconnect). A public visor
creates and maintains its own `visor` entry. It does not depend on any other
component to register it.

## Addressing

The advertised address is `<visor-pk>:<port>`, where the port is the local port
of the visor's `stcpr` client. The entry is registered with Service Discovery
as described in Service Discovery.

## Data Formats

### Configuration

`visorconfig.V1.IsPublic` and `visorconfig.PublicVisorConfig`.

| JSON key                           | Type     | Default  | Meaning                                  |
|------------------------------------|----------|----------|------------------------------------------|
| `is_public`                        | bool     | false (`skyenv.IsPublic`) | enables public visor mode |
| `public_visor.registration_timeout`| duration | 10 min (`skyenv.PublicVisorRegistrationTimeout`) | time allowed to receive an external `stcpr` connection; 0 disables the check |
| `public_visor.max_transports`      | int      | 2048 (`visorconfig.PublicVisorMaxTransports`) | distinct peer count that pauses registration; 0 disables the limit |

### Service Discovery entry

`servicedisc.Service` of type `visor` (`ServiceTypeVisor`) with `address` set to
the key and port above, and the visor's geo data and version.

## Behaviour

### Startup

`initPublicVisor` first removes any `visor` entry the visor left at its previous
run. If `is_public` is false it ends there. Otherwise it reads the port of the
`stcpr` client, creates an `appdisc.PublicVisorUpdater` with the configured
timeout and limit, and starts it. The updater registers the entry in the
background and repeats the registration every `skyenv.ServiceDiscUpdateInterval`,
90 s. A failed registration is retried at the next interval.

### Validation

A visor is validated when its `stcpr` listener accepts a connection whose remote
address is not private. A private address is loopback, link-local, IPv4
`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` or `100.64.0.0/10`, or IPv6
`fc00::/7` (`network.isPrivateIP`).

If `registration_timeout` is above 0 and no such connection arrives within it,
the updater deletes the entry and stops. It does not register again until the
visor restarts. A visor validated before the timeout is not checked again.

### Distinct peer limit

Once a minute the updater counts the distinct remote visors across all the
visor's transports. Several transports to one peer count once.

| State   | Condition                                   | Action                                  |
|---------|---------------------------------------------|-----------------------------------------|
| active  | count >= `max_transports`                   | delete the entry, pause the heartbeat   |
| paused  | count < 90 percent of `max_transports`      | register at once, resume the heartbeat  |

A paused visor keeps its transports and keeps accepting connections.

## Security Considerations

A public visor accepts transports from any visor. Transport encryption and
authentication are those of the transport protocol. The entry lists the visor's
address, so the address is visible to every Service Discovery client.

## References

- Service Discovery
- Public Autoconnect
- Address Resolver Privacy
