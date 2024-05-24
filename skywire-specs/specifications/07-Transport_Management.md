# Transport Management

For all Skywire Node types, we need a universal way for managing and logging Transports. The structure that is responsible for this is the `TransportManager` (which should be within the `/pkg/node` package of the `skywire` package).

As the `TransportManager` needs to interact with the *Transport Discovery* and other Skywire Nodes, it should have access to the local node's public and private key identity.

The following is a proposed implementation of `TransportManager`;

```golang
package node

// Transport wraps a 'transport.Transport' implementation and contains 
// associated/useful for the 'transport.Transport' implementation.
type Transport struct {
    transport.Transport
    ID          uuid.UUID
    // more fields ...
}

// TransportManagerConfig configures a TransportManager.
type TransportManagerConfig struct {
	PubKey          cipher.PubKey     // Local PubKey
	SecKey          cipher.SecKey     // Local SecKey
	DiscoveryClient client.Client     // Transport discovery client
	LogStore        TransportLogStore // Store for transport's transfer rates
}

// TransportManager manages Transports.
type TransportManager struct {
    // Members...
}

// NewTransportManager creates a TransportManager with the provided configuration and transport factories.
// 'factories' should be ordered by preference.
func NewTransportManager(config *TransportManagerConfig, factories ...transport.Factory) (*TransportManager, error) { /* ... */ }

// Start starts the transport manager.
// - 'ctx' can end the transport listening operation.
func (tm *TransportManager) Serve(ctx context.Context) error { /* ... */ }

// Observe returns channel for notifications about new Transport
// registration. Only single observer can listen for on a channel.
func (tm *TransportManager) Observe() <-chan *Transport { /* ... */ }

// Factories returns all the factory types contained within the TransportManager.
func (tm *TransportManager) Factories() []string { /* ... */ }

// Transport obtains a Transport via a given Transport ID.
func (tm *TransportManager) Transport(id uuid.UUID) (*Transport, bool) { /* ... */ }

// RangeTransports ranges all Transports.
// Should return when 'action' returns a non-nil error.
func (tm *TransportManager) RangeAllTransports(action TransportAction) error { /* ... */ }

// CreateTransport begins to attempt to establish transports to the given 'remote' node.
// This should be a non-blocking operation and any failures or future Transport disconnections
// should be dealt with with retries (under a given time interval).
// - 'remote' specifies the remote node to attempt to establish the Transports with.
// - 'tpType' is the transport type that is to be created.
// - 'public' determines whether the Transports established should be advertised to Transport Discovery.
// If a transport is not to be public, a random transport ID is assigned.
func (tm *TransportManager) CreateTransport(ctx context.Context, remote cipher.PubKey, tpType string, public bool) (*Transport, error) { /* ... */ }

// DeleteTransport disconnects and removes the Transport of Transport ID.
func (tm *TransportManager) DeleteTransport(id uuid.UUID) error { /* ... */ }
```

## Transport Manager Procedures

The transport manager is responsible for keeping track of established transports (via the `transport.Entry` and the `transport.Status` structures). The `transport.Entry` structure describes and identifies transports, while `transport.Status` keeps track of whether the transport is up or down (based on the perspective of the local node).

If the *Transport Manager* wishes to confirm transport information, it can query the *Transport Discovery* via the `GET /transports/edge:<public-key>` endpoint. Note that it is expected of the *Transport Manager* to call this endpoint on startup.

When a transport is "closed" it is only considered "down", not "destroyed".

The following highlights detailed startup and shutdown procedures of a *Transport Manager*;

**Startup:**

On startup, the `TransportManager` should call the *Transport Discovery* to ensure that it is up to date. Then it needs to attempt to establish (or re-establish) transports to the relevant remote nodes.

When re-establishing a Transport, the `transport.Entry` used should be that also previously stored in the *Transport Discovery*.

Once connected, the `TransportManager` should update it's *Status* of the given Transport and set `is_up` to `true`.

The startup logic is triggered when `Start` is called.

**Shutdown:**

On shutdown, the first step is to update the *Transport Statuses* to "down" via the *Transport Discovery*. Then Transports to remote nodes is to be closed (with a timeout, in which after, the transport in question is forcefully closed).

## Logging

A *Transport Manager* is responsible for logging incoming and outgoing communication for each transport. Initially, only the total incoming and outgoing bandwidth (in bytes) is to be logged per transport.

```golang
// TransportLogEntry represents a logging entry for a given Transport.
// The entry is updated every time a packet is received or sent.
type TransportLogEntry struct {
    ReceivedBytes big.Int      // Total received bytes.
    SentBytes     big.Int      // Total sent bytes.
}
```

Logs for each transport is to be stored using `TransportLogStore`. `TransportLogStore` is to be specified within `TransportManagerConfig`.

```golang
// TransportLogStore stores transport log entries.
type TransportLogStore interface {
	Entry(id uuid.UUID) (*TransportLogEntry, error)
	Record(id uuid.UUID, entry *TransportLogEntry) error
}
```
