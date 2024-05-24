# Drafts

## Transport State

The modifiable data associated with a *Transport* is stored in a different structure called the *Transport State*. As a *Transport* has two edges (and hence, two perspectives), a *Transport State* is also updated accordingly.

## Packet types

Transports, Routes and the *Route Setup Service* are to deliver data via Packets. Packets are responsible for setting up routes and streams, and delivering data within the constructed routes and streams.

Here is a summary of all the *Packet Types*.

| Type |  Value | Description |
| ---- | ------ | ----------- |
| `Ping` | `0x0` | Sent between a Transport to check if connection is still open, and to determine latency of the Transport. |
| `InitiateRoute` | `0x1` | First packet sent via a route to have it initiated. |
| `RouteInitiated` | `0x2` | Confirms that a route is set up and functional. |
| `DestroyRoute` | `0x3` | Initiate the destruction of a route. |
| `RouteDestroyed` | `0x4` | Confirm the success of a route's destruction. |
| `OpenStream` | `0x5` | Opens a stream within a route. |
| `StreamOpened` | `0x6` | Confirms that a stream is successfully opened. |
| `CloseStream` | `0x7` | Closes a stream. |
| `StreamClosed` | `0x8` | Informs that a stream has successfully closed. |
| `Forward` | `0x9` | Forwards data via a specified stream. |

## Route Setup Service

The *Route Setup Service* is a service which communicates with ...