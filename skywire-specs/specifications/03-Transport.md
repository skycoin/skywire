# Transport

A *Transport* represents a bidirectional line of communication between two *Skywire Nodes* (or *Transport Edges*).

Each *Transport* is represented as a unique 16 byte (128 bit) UUID value called the *Transport ID* and has a *Transport Type* that identifies a specific implementation of the *Transport*.

A *Transport* has the following information associated with it;

- **Transport ID:** A `uuid.UUID` value that uniquely identifies the Transport.
- **Edges:** The public keys of the Transport's edge nodes (should only have 2 edges and the initiating edge should come first).
- **Type:** A `string` value that specifies the particular implementation of the *Transport*.
- **Public:** A `bool` that specifies whether the *Transport* is to be registered in the *Transport Discovery* or not. Only public transports are registered.
- **Registered:** A `int64` value that is the epoch time of when the *Transport* is registered in *Transport Discovery*. A value of `0` represents the state where the *Transport* is not (or not yet) registered in the *Transport Discovery*.

This is a JSON representation of a *Transport Entry*;

```json
{
    "t_id": "e1808c316b23d1d6119cad1795238ff0",
    "edges": ["031d796272349d597d6d3130497ccd11cf8af12c7d186b1726358abfb49edad0c1", "03bd9724f335c5eb5a1011e7862d4af28488102c8edffc84585cf0826ac4864b38"],
    "type": "messaging",
    "public": true
}
```

## Transport Module

In code, `Transport` is an interface, and can have many implementations.

The interface used to generate *Transports* of a certain *Transport Type* is named *Transport Factory* (represented by a `transport.Factory` interface in code).

The representation of a *Transport* in *Transport Discovery* is of the type `transport.Entry`.

A `transport.Status` type contains the status of a given *Transport*. Each *Transport Edge* provides such status, and the *Transport Discovery* compares the two statuses to derive the final status.

```golang
package transport

// Transport represents communication between two nodes via a single hop.
type Transport interface {

    // Read implements io.Reader
    Read(p []byte) (n int, err error)

    // Write implements io.Writer
    Write(p []byte) (n int, err error)

    // Close implements io.Closer
    Close() error

    // Local returns the local transport edge's public key.
    Local() cipher.PubKey

    // Remote returns the remote transport edge's public key.
    Remote() cipher.PubKey

    // Type returns the string representation of the transport type.
    Type() string

    // SetDeadline functions the same as that from net.Conn
    // With a Transport, we don't have a distinction between write and read timeouts.
    SetDeadline(t time.Time) error
}

// Factory generates Transports of a certain type.
type Factory interface {

    // Accept accepts a remotely-initiated Transport.
    Accept(ctx context.Context) (Transport, error)

    // Dial initiates a Transport with a remote node.
    Dial(ctx context.Context, remote cipher.PubKey) (Transport, error)

    // Close implements io.Closer
    Close() error

    // Local returns the local public key.
    Local() cipher.PubKey

    // Type returns the Transport type.
    Type() string
}

// Entry is the unsigned representation of a Transport.
type Entry struct {

    // ID is the Transport ID that uniquely identifies the Transport.
    ID uuid.UUID `json:"tid"`

    // Edges contains the public keys of the Transport's edge nodes (the public key of the node that initiated the transport should be on index 0).
    Edges [2]string `json:"edges"`

    // Type represents the transport type.
    Type string `json:"type"`

    // Public determines whether the transport is to be exposed to other nodes or not.
    // Public transports are to be registered in the Transport Discovery.
    Public bool `json:"public"`
}

// SignedEntry holds an Entry and it's associated signatures.
// The signatures should be ordered as the contained 'Entry.Edges'.
type SignedEntry struct {
    Entry      *Entry    `json:"entry"`
    Signatures [2]string `json:"signatures"`
    Registered int64     `json:"registered,omitempty"`
}

// Status represents the current state of a Transport from the perspective
// from a Transport's single edge. Each Transport will have two perspectives;
// one from each of it's edges.
type Status struct {

    // ID is the Transport ID that identifies the Transport that this status is regarding.
    ID uuid.UUID `json:"tid"`

    // IsUp represents whether the Transport is up.
    // A Transport that is down will fail to forward Packets.
    IsUp bool `json:"is_up"`

    // Updated is the epoch timestamp of when the status is last updated.
    Updated int64 `json:"updated,omitempty"`
}
```
