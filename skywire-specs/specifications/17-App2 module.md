# `app2`

The current `app` module of Skywire is a mess. It is hard to test and it's actual communication logic is split across multiple packages. This proposal details how I would envision an *ideal* `app` package, and also how we can migrate and eventually scrap the old `app` module.

## Overview

The visor node is the intermediary of communication between apps and the network(s). The `app2` package will provide structures for use in `visor` and skywire apps. It will facilitate the communication between them. All networks used by skywire should implement the `net.Conn` and `net.Listener` interfaces. Currently, apps only need to dial/accept connections on `dmsg` and `router`.

The goal of `app2` is to be simple and retain as much of the original form of the communication as possible. This should improve code readability and performance (when compared to the original `app` package).

`app2` will have two major components: `app2.Client` (which will be used by skywire apps) and `app2.Server` (which will run on the visor node). The communication between `app2.Client` and `app2.Server` should be via unix socket where the `app2.Server` is responsible for listening on a socket file.

## Types

The following structures should identify the components that facilitate interation between `app2.Client` and `app2.Server`.

### Summary

```golang
package app2

// ProcID identifies the current instance of an app (an app process).
// The visor node is responsible for starting apps, and the started process
// should be provided with a ProcID.
type ProcID uint16

// HSFrameType identifies the type of handshake frame.
type HSFrameType byte

// HSFrame is the data unit for socket connection handshakes between Server and Client.
type HSFrame []byte

// Client is used by skywire apps.
type Client struct {
    // TODO: define.
}

// NewClient creates a new Client. The Client needs to be provided with:
// - localPK: The local public key of the parent skywire visor.
// - pid: The procID assigned for the process that Client is being used by.
// - sockAddr: The socket address to connect to Server.
func NewClient(localPK cipher.PubKey, pid ProcID, sockAddr string) (*Client, error) {
    // TODO: define.
}

// Server is used by skywire visor.
type Server struct {
    // TODO: define.
}
```

The reason for only having handshake frames (`HSFrame`), is because a connection on a network of choice between a visor node and a remote node, is represented by a socket connection between the visor node (via `app2.Server`) to a skywire app (which uses `app2.Client`). So after the handshake (which is used for determining how we map the socket connection), the data thereafter can just be forwarded as-is to and from the socket connection and the network connection.

### `HSFrame`

A `HSFrame` consists of a header and a body. The contents of the body changes based on the `HSFrameType` (specified in the header).

**`HSFrame` header:**

The `HSFrame` header consists of a `ProcID`, a `HSFrameType` and a `BodyLen` (5 bytes in total).

```
| ProcID (2 bytes) | HSFrameType (1 byte) | BodyLen (2 bytes) |
```

**`HSFrame` body:**

The `HSFrame` body should be a JSON structure.

| HSFrameType Name | HSFrameType Value | HSFrame Body Contents | Usage(s) |
| --- | --- | --- | --- |
| `DmsgListen` | `10` | localPK, localPort | `Client -> Server` to request listening on a given pk/port pair. |
| `DmsgListening` | `11` | localPK, localPort | `Server -> Client` to inform that the client is now listening the pk/port pair. |
| `DmsgDial` | `12` | localPK, (localPort), remotePK, remotePort | `Client -> Server` to request dialing to a remote.<br>`Server -> Client` to inform that a remote has dialed.<br>**Note:** localPort must be `0` when `Client -> Server` as the `app2.Server` will provide an ephemeral port on `DmsgAccept`. |
| `DmsgAccept` | `13` | localPK, localPort, remotePK, remotePort | `Client -> Server` to inform the server that the remote connection is accepted.<br>`Server -> Client` to inform the client that the connection is accepted by remote. |

Note that there are no *close* frame types, as one can just close the underlying socket connection.

## Procedures

### `app2.Client` listens on given pk/port.

- `app2.Client` dials a new socket connection to `app2.Server` and sends a `DmsgListen` type `HSFrame` with local pk/port in which the client wishes to listen on.
- `app2.Server` calls `(*dmsg.Client).Listen` and creates a `dmsg.Listener`.
  - If there is no error, `app2.Server` sends `DmsgListening` back to the client. 
  - On failure, the `app2.Server` just closes the socket connection.

### `app2.Client` accepts a connection from remote.

Given that an `app2.Client` is listening on a given pk/port, when `app2.Server` accepts a remote connection, the following should happen:

- `app2.Server` sends `DmsgDial` to `app2.Client` via the socket connection that is initiated with `DmsgListen`.
- `app2.Client` dials a new socket connection to `app2.Server` and sends `DmsgAccept` (with the same body as the `DmsgDial`) via the new socket connection.
- Further data to/from the related remote connection should be directly forwarded by this new socket connection.

### `app2.Client` dials to a remote pk/port.

- `app2.Client` dials a new socket connection to the `app2.Server` and sends a `DmsgDial` with an empty localPort.
- `app2.Server` attempts the requested dial. 
  - On success, the `app2.Server` sends `DmsgAccept` to `app2.Client` via the same socket connection.
  - On failure, the socket connection gets closed.

## Implementation notes

### `app2.Server`
- We will need to move most of the app-management logic here. Currently, they are mostly located in [`visor`](https://github.com/skycoin/skywire/blob/mainnet-milestone1/pkg/visor/visor.go). On top of this, we need to introduce the idea of `ProcID`. We should probably add a new structure (named `Process`) that holds both the `os.Process` and `net.Conn` socket connection to the app pocess.