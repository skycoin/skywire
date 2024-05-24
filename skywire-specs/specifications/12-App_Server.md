# App Server

The `AppServer` (located within the `skywire/pkg/node` module) handles communication between local and remote Apps. It also manages and administers local Apps. It is to interact with a `Router` and identify loops via the *App* routing rule retrieved from the routing table. The *App* rule is structured as follows;

```
| expiry  | r-type | resp-rid | remote-pk | remote-port | local-port |
| 8 bytes | 1 byte | 4 bytes  | 33 bytes  | 2 bytes     | 2 bytes    |
```

The *App Server* not only forwards Packets between Apps and remote entities, but it can also be instructed by the end user to send *Quit* signals to the Apps. *Apps* can also request to open *Loops* with remote/local Apps.

Each *Loop* is identified uniquely via the local and remote public keys, paired with the local and remote ports.

Within the `AppServerConfig` file, local ports are reserved for certain Apps. The following rules are to be opposed:
- Ports are either "reserved" or "unreserved".
    - No two Apps are allowed to "reserve" the same port.
    - Ports are reserved via the `AppServerConfig` file.
- Reserved ports are either "active" or "inactive".
    - A port is "active" when the port is "reserved" for an App, and that App is running.
    - A port is "inactive" either when the port is "unclaimed", or when the port is "claimed" but the App is not running.

The following communication processes between a given App and the *App Server* is to exist:

- **App requests to open loop**
- **App Server asks App whether it wishes to respond to a remotely-initiated loop**
- **App Server informs App that loop is to be closed (with given reason)**
  - Reasons include: Route timeout, remotely closed, locally closed, etc.
- **App informs App Server it wishes to close a loop (with given reason)**
  - Reasons include: App is shutting down, loop no-longer used, etc.
- **App Server forwards packets received from remote App**
  - If local app does not exist or does not accept, the loop is to be closed, and routes destroyed.
- **App Server forwards packets received from local App** 
  - If the rule does not exist, or the remote app nodes not accept, the loop is to be closed and routes destroyed.

## Loop Encryption

Each loop is to be symmetrically encrypted with [Noise](http://noiseprotocol.org/noise.html). Specifically, we are to use the `KK` fundamental [handshake pattern](http://noiseprotocol.org/noise.html#handshake-patterns). The first noise message should be provided by initiator in the request to create a new loop, this messages will be setup to responder in a loop confirmation request. Responder should send second noise message which will be returned to initiator in a loop confirmation request.
