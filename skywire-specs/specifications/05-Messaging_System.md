# Messaging System

The messaging system is an initial implementation of the `Transport` and associated interfaces. To work, the messaging system requires an active internet connection and is designed to be horizontally scalable.

Three services make up the messaging system: *Messaging Client* (or *Client Instance*), *Messaging Server* (or *Service Instance*) and *Messaging Discovery*.

*Messaging Clients* and *Messaging Servers* are represented by public/private key pairs. *Messaging Clients* deliver data to one another via *Messaging Servers* which act as relays.

The *Messaging Discovery* is responsible for allowing *Messaging Clients* to find other advertised *Messaging Clients* via their public keys. It is also responsible for finding appropriate *Messaging Servers* that either "advertise" them, or "advertise" other *Messaging Clients*.

```
           [D]

     S(1)        S(2)
   //   \\      //   \\
  //     \\    //     \\
 C(A)    C(B) C(C)    C(D)
```

Legend:
- ` [D]` - Discovery Service
- `S(X)` - Messaging Server (Server Instance)
- `C(X)` - Messaging Client (Client Instance)

## Messaging System Modules

There are two modules of the messaging system.

- `messaging-discovery` contains the implementation of the *Messaging Discovery*.
- `messaging` contains the implementation of either a *Client Instance* or a *Server Instance* of a *Messaging Client* or *Messaging Server*.

## Messaging Procedures

This is a summary of the procedures that the *Messaging System* is to handle.

**Advertising a client:**

To be discoverable by other clients, a client needs to advertise itself.

1. Client queries the Discovery to find available Servers.
2. Client connects to some (or all) of the suggested Servers.
3. Client updates it's own record in Discovery to include it's delegated Servers.

**Client creates a channel to another client:**

In order for two clients to communicate, both clients need to be connected to the same messaging server, and create a channel to each other via the server.

1. Client queries discovery of the remote client's connected servers. The client will connect to one of these servers if it originally has no shared servers with the remote.
2. The client sends a `OpenChannel` frame to the remote client via the shared server.
3. If the remote client accepts, a `ChannelOpened` frame is sent back to the initiating client (also via the shared server). A channel is represented via two *Channel IDs* (between the initiating client and the server, and between the responding client and the server). The associated between the two channel IDs is defined within the server.
4. Once a channel is created, clients can communicate via one another via the channel.

## Messaging Discovery

The *Messaging Discovery* acts like a DNS for messaging instances (*Messaging Clients* or *Messaging Servers*).

### Instance Entry

An entry within the *Messaging Discovery* can either represent a *Messaging Server* or a *Messaging Client*. The *Messaging Discovery* is a key-value store, in which entries (of either server or client) use their public keys as their "key".

The following is the representation of an Entry in Golang.

```golang
// Entry represents an Instance's entry in the Discovery database.
type Entry struct {
    // The data structure's version.
    Version string `json:"version"` 

    // A Entry of a given public key may need to iterate. This is the iteration sequence.
    Sequence uint64 `json:"sequence"` 

    // Timestamp of the current iteration.
    Timestamp int64 `json:"timestamp"`

    // Public key that represents the Instance.
    Static string `json:"static"`

    // Contains the node's required client meta if it's to be advertised as a Messaging Client.
    Client *Client `json:"client,omitempty"`

    // Contains the node's required server meta if it's to be advertised as a Messaging Server.
    Server *Server `json:"server,omitempty"`

    // Signature for proving authenticity of of the Entry.
    Signature string `json:"signature,omitempty"`
}

// Client contains the node's required client meta, if it is to be advertised as a Messaging Client.
type Client struct {
    // DelegatedServers contains a list of delegated servers represented by their public keys.
    DelegatedServers []string `json:"delegated_servers"`
}

// Server contains the node's required server meta, if it is to be advertised as a Messaging Server.
type Server struct {
    // IPv4 or IPv6 public address of the Messaging Server.
    Address string `json:"address"`

    // Port in which the Messaging Server is listening for connections.
    Port string `json:"port"`

    // Number of connections still available.
    AvailableConnections int `json:"available_connections"`
}
```

**Definition rules:**

- A record **MUST** have either a "Server" field, a "Client" field, or both "Server" and "Client" fields. In other words, a Messaging Node can be a Messaging Server Node, a Messaging Client Node, or both a Messaging Server Node and a Messaging Client Node.

**Iteration rules:**

- The first entry submitted of a given static public key, needs to have a "Sequence" value of `0`. Any future entries (of the same static public key) need to have a "Sequence" value of `{previous_sequence} + 1`.
- The "Timestamp" field of an entry, must be of a higher value than the "Timestamp" value of the previous entry.

**Signature Rules:**

The "Signature" field authenticates the entry. This is the process of generating a signature of the entry:
1. Obtain a JSON representation of the Entry, in which:
    1. There is no whitespace (no ` ` or `\n` characters).
    2. The `"signature"` field is non-existent.
2. Hash this JSON representation, ensuring the above rules.
3. Create a Signature of the hash using the node's static secret key.

The process of verifying an entry's signature will be similar.

### Store Interface

The underlying database of the *Messaging Discovery* is a key-value store. The `Store` interface allows many databases to be used with the *Messaging Discovery*. 

```golang
type Store interface {
    // Entry obtains a single messaging instance entry.
    // 'static' is a hex representation of the public key identifying the messaging instance.
    Entry(ctx context.Context, static string) (*Entry, error)

    // SetEntry set's an entry.
    // This is unsafe and does not check signature.
    SetEntry(ctx context.Context, entry *Entry) error

    // AvailableServers discovers available messaging servers.
    // Obtains at most 'maxCount' amount of available servers obtained randomly.  
    AvailableServers(ctx context.Context, maxCount int) ([]*Entry, error)
}
```

### Endpoints

Only 3 endpoints need to be defined; Get Entry, Post Entry, and Get Available Servers.

#### GET Entry
Obtains a messaging node's entry.
> `GET {domain}/discovery/entries/{public_key}`

**REQUEST**

Header:
```
Accept: application/json
```

**RESPONSE**

Possible Status Codes:
- Success (200) - Successfully updated record.
    - Header:
        ```
        Content-Type: application/json
        ```
    - Body:
        > JSON-encoded entry.
- Not Found (404) - Entry of public key is not found.
- Unauthorized (401) - invalid signature.
- Internal Server Error (500) - something unexpected happened.

#### POST Entry
Posts an entry and replaces the current entry if valid.
> `POST {domain}/discovery/entries`

**REQUEST**

Header:
```
Content-Type: application/json
```
Body:
> JSON-encoded, signed Entry.

**RESPONSE**

Possible Response Codes:
- Success (200) - Successfully registered record.
- Unauthorized (401) - invalid signature.
- Internal Server Error (500) - something unexpected happened.

#### GET Available Servers
Obtains a subset of available server entries.
> `GET {domain}/discovery/available_servers`

**REQUEST**

Header:
```
Accept: application/json
```

**RESPONSE**

Possible Status Codes:
- Success (200) - Got results.
    - Header:
        ```
        Content-Type: application/json
        ``` 
    - Body:
        > JSON-encoded `[]Entry`.
- Not Found (404) - No results.
- Forbidden (403) - When access is forbidden.
- Internal Server Error (500) - Something unexpected happened.

### Messaging Discovery Client Library

The module is named `client`. It contains a `HTTPClient` structure, that defines how the client will interact with the *Messaging Discovery* API. 

A new `HTTPClient` object can be instantiated using the public function `New(address string)`.

It exposes the following public methods:

```go
// Entry retrieves an entry associated to the given public key from the discovery server.
func (*HTTPClient) Entry(ctx context.Context, static string) (*Entry, error) {
    // definition ...
}

// SetEntry tries to set the given entry on the discovery server. It must be signed.
// If the entry is modifying a previous one, must be signed by the same private key.
func (*HTTPClient) SetEntry(ctx context.Context, entry *Entry) error {
    // definition ...
}

// AvailableServers gets a list of server entries from the skywire discovery server.
// The amount is determined by the discovery server.
func (*HTTPClient) AvailableServers(ctx context.Context) ([]*Entry, error) {
    // definition ...
}
```

The module also provides public functions to instantiate valid `Entry` objects.

### Messaging Discovery Integration Tests

> **TODO:** Fix wording.

This package does not uses another `messenger` package, so integration tests are defined for the external services that `discovery` is using. In this case, the external store and `discovery` itself for testing the `client` library.

The cases for the store integration testing:

 1. Its able to set an entry on the database without error by calling the `storer.SetEntry` method.
 2. Its able to retrieve the previously set entry by calling the `storer.Entry` method.
 3. Creates multiple service entries and store them by calling `storer.SetEntry`, then it should be able to retrieve them with `store.AvailableServers`.
 4. `store.AvailableServers` receives a `maxCount int` argument. We also test it passing an integer which value is less than the amount of server entries we have set in the database, it should return this exact amount of server entries.
 5. Same as in number 4, but we set `maxCount` to number bigger than the number of server entries we have set, now we should get an slice of the size of the server entries we have set.

In order to run the test we preferably create a clean new instance of the store database using Docker, and the test code should connect to it. We remove it after we have tested.

In order to test the client library we do integration test with an instance of the discovery server.
The test cases for the client integration testing:

1. By using the method SetEntry the client can set a new entry on the discovery server.
2. By using the method SetEntry the client can update a previously set entry on the discovery server.
3. If using SetEntry to update a previously set Entry, but the new sequence is not the previous sequence + 1 it should return an error with status code 500, Something unexpected happened.
4. If using SetEntry to update a previously set Entry, but the signature of the new entry has been made by a different secret key it should return an error with status code 401, Invalid signature.
5. By calling the method Entry with the public key of a previously set Entry it should return that entry.
6. By calling the method Entry with the public key of a previously non-set Entry it should return an error with code 404, Entry of public key is not found.
7. By calling the method AvailableServers when there are previously set server entries it should return them.

## Messaging Link

The `link` provides two *Messaging Instances* a means to establish a connection with one another, and also handle a pool of connections.

Using the *Messaging Discovery*, a *Messaging Instance* can discover other instances via only their public key. However, a *Link* requires both a public key and an address:port.

Data sent via a *Link* is encapsulated in *Frames*. A *Link* is implemented using a TCP connection.

### Link Handshake Frames

When setting up a *Link* between two instances, the instance that initiates is called the *Initiator* and the instance that responds is called the *Responder*. Each instance is represented by a public key.

To set up a *Link*, the *Initiator* first dials a TCP connection to the listening *Responder*. Once the TCP connection is established, the *Responder* sends the first *Frame*. It is expected that the *Initiator* knows the public key of the *Responder*

Given a situation where instances 'A' and 'B' are to establish a link with one another (where 'A' is the initiator), the following *Frames* are delivered to perform a handshake.

Link Handshake Frames are to be in JSON format.

**Link Handshake Frame 1 (A -> B):**

```json
{
    "version": "0.1",
    "initiator": "036b630436972743d4b3f5bb39cd451da29d222b5d30893684a40f34a66c692157",
    "responder": "02e18998f0174631710e47052927e13bddc712ca0c11289e0150fbf57570e31151",
    "nonce": "853ea8454d11bd3b59cb31f8572a3779"
}
```

The initiator is responsible for sending the first frame.
- `"version"` specifies the version of messaging protocol that the initiator is using (`"0.1"` for now).
- `"initiator"` should contain the hex representation of the public key of the initiator (the instance that is sending the first handshake frame).
- `"responder"` should contain the hex representation of the public key of the expected responder (the responder should disconnect TCP if this is not their public key).
- `"nonce"` is the hex-string representation of a 16-byte nonce that the responder should sign (alongside the initiator's public key) to check authenticity of the responder and whether the responder.

**Link Handshake Frame 2 (B -> A):**

```json
{
    "version": "0.1",
    "initiator": "036b630436972743d4b3f5bb39cd451da29d222b5d30893684a40f34a66c692157",
    "responder": "02e18998f0174631710e47052927e13bddc712ca0c11289e0150fbf57570e31151",
    "nonce": "853ea8454d11bd3b59cb31f8572a3779",
    "sig1": "df8a978f0ea681e218cfd8127692dbe4190441567181b9057ab15da34b08ff610d9060e5195419e1744bb57d50373c1dd444b5c2753a80dba32b292fa306e9df01"
}
```

This frame allows the responder agree with the initiator and prove it's ownership of it's claimed public key.

The `"sig1"` field contains a hex representation of the result of signing the concatenation of the version, initiator, responder and nonce fields. Note that before concatenation, hex representations should be decoded and the concatenation result needs to be hashed before being signed. `"sig1"` should be signed by the responder.

**Link Handshake Frame 3 (A -> B):**

```json
{
    "version": "0.1",
    "initiator": "036b630436972743d4b3f5bb39cd451da29d222b5d30893684a40f34a66c692157",
    "responder": "02e18998f0174631710e47052927e13bddc712ca0c11289e0150fbf57570e31151",
    "nonce": "853ea8454d11bd3b59cb31f8572a3779",
    "sig1": "df8a978f0ea681e218cfd8127692dbe4190441567181b9057ab15da34b08ff610d9060e5195419e1744bb57d50373c1dd444b5c2753a80dba32b292fa306e9df01",
    "sig2": "fc17928d5a3f7691434282fb3108d1603889f996e8e45adc2e35362e08009b8611abb9f45e511b9931f0f04b37ff1057fd69554befe534ad28c77ff0c44121ab00"
}
```

This frame allows the initiator to inform the responder that `"sig1"` is accepted, and to prove the initiator's ownership of it's public key.

`"sig2"` is the signature result of the concatenation of the version, initiator, responder, nonce and sig1 fields. Concatenation rules are the same as that of `"sig1"`.

**Link Handshake Frame 4 (B -> A):**

```json
{
    "version": "0.1",
    "initiator": "036b630436972743d4b3f5bb39cd451da29d222b5d30893684a40f34a66c692157",
    "responder": "02e18998f0174631710e47052927e13bddc712ca0c11289e0150fbf57570e31151",
    "nonce": "853ea8454d11bd3b59cb31f8572a3779",
    "sig1": "df8a978f0ea681e218cfd8127692dbe4190441567181b9057ab15da34b08ff610d9060e5195419e1744bb57d50373c1dd444b5c2753a80dba32b292fa306e9df01",
    "sig2": "fc17928d5a3f7691434282fb3108d1603889f996e8e45adc2e35362e08009b8611abb9f45e511b9931f0f04b37ff1057fd69554befe534ad28c77ff0c44121ab00",
    "accepted": true
}
```

Sent by the responder, this frame concludes the handshake if the value of `"accepted"` is `true`.

### Messaging Frames

After the handshake phase, frames have a reoccurring format. These are the *Messaging Frames* of the messaging system.

```
| FrameType | PayloadSize | Payload |
| 1 byte    | 2 bytes     | ~ bytes |
```

- The `Type` specifies the frame type. Different frame types are used for opening and closing channels as well as sending packets via the channels.
- `PayloadSize` contains an encoded `uint16` value that represents the Payload's length (the max size is 65535).
- `Payload` has a length determined by `PayloadSize`.

The following is a summary of the frame types.

| FrameTypeValue | FrameTypeName | FrameBody |
| -------------- | ------------- | --------- |
| `0x0` | `OpenChannel` | ChannelID + RemoteStatic + NoiseMessage1 |
| `0x1` | `ChannelOpened` | ChannelID + NoiseMessage2 |
| `0x2` | `CloseChannel` | ChannelID |
| `0x3` | `ChannelClosed` | ChannelID |
| `0x4` | `Send` | ChannelID + CipherText |

The `FrameBody` has the following sub-fields. A `FrameBody` with multiple sub-fields have the sub-fields concatenated.

- The `ChannelID` sub-field is represented by a single byte. This restricts a *Client Instance* to have at most 256 channels via a single *Server Instance*.
- The `RemoteStatic` sub-field is represented by 33 bytes. It contains a public key of a remote *Client Instance*.
- `NoiseMessage1` and `NoiseMessage2` are both represented by 49 bytes. It contains the noise handshake messages for establishing symmetric encryption between the two client instances of the channel. The noise handshake pattern used is KK.
- The `CipherText` sub-field is the only sub-field with a modular length. It contains size of the encrypted payload followed by payload that is to be delivered.

### Noise Implementation in Channels

As stated above, a channel is established using the `OpenChannel` and `ChannelOpened` frames. Then, after a channel is established, the two *Client Instances* of the channel can communicate with each over via `Send` frames (which includes a `CipherText` component).

The protocol used to establish the symmetric encryption of the `CipherText` is the [Noise Protocol](http://noiseprotocol.org/).

The curve used will be `secp256k1` for the key pair, and `chacha20poly1305` will be used for the symmetric encryption itself.

Note that, the noise protocol requires the public key length and the ECDH result length (shared secret) to be equal. Because for `secp256k1`, public keys have a length of 33, and the ECDH result has a length of 32, so an empty byte (`0x0`) should be appended to all generated ECDH results. Hence, the `DHLEN` constant for the noise protocol should be 33.

After the handshake, the CipherState object will be used by the *Client Instances* to encrypt and decrypt the `CipherText` contained within the `Send` frame.

**Handshake pattern:**

Only the `KK` [interactive handshake pattern (fundamental)](http://noiseprotocol.org/noise.html#interactive-handshake-patterns-fundamental) will be supported.

```
-> s
<- s
...
-> e, es, ss
<- e, ee, se
```

The `-> e, es, ss` message is the `NoiseMessage1` of a `OpenChannel` frame, while the `<- e, ee, se` message is the `NoiseMessage2` of a `ChannelOpened` frame.

### Implementation in Code

Within the `messaging` module:

- `Link` structure should represent a link between two instances.
- `Pool` structure should handle multiple `Links` (with different instances).
- `Client` which implements a *Client Instance*.
- `Server` which implements a *Server Instance*.

*Client Instances* communicate with each other via a *Server Instance* (which acts as a relay).

Both structs will use `link.Pool` to handle links, but *Frames* are handled differently. *Client Instances* are to implement `TransportFactory` while a *Server Instance* is not required to. A *Client Instance* should also represent an established *Channel* as a `Transport` implementation.

### Configuring an Instance

When creating an *Instance*, the following options should be available via the following structure.

```golang
// Config configures an instance.
type Config struct {
    // Public determines whether the instance is to advertise itself to the messaging discovery servers.
    Public bool

    // DiscoveryAddresses contains the messaging discovery services to be used (in order of preference).
    DiscoveryAddresses []string
}
```

The above structure is to be an input when creating a *Server Instance* or a *Client Instance*.

### Instance Interaction with Messaging Discovery

On startup `Server` that is supposed to be publicly available should register itself in messaging discovery. To do so it first has to fetch current version of an `Entry`, if entry doesn't exist it should create one. If entry exists it may update it if necessary.

On startup `Client` may connect to necessary amount of servers by fetching list of available servers from the discovery. Once initial connections are established `Client` should update discovery entry to advertise it's relays.

### Channel Management

The following is a proposal of how a Channel can be represented in code.

```golang
// Channel represents a channel that implements Transport. It can be from the perspective of a Server or Client Instance.
type Channel struct {
    // ChannelID represents the ID that is associated of the adjacent link.
    ChannelID uint8

    // Destination is the public key of the instance that is the final destination.
    // This should always contain the public key of a client instance (as a server cannot be the final destination).
    Destination cipher.PubKey

    // Link contains the adjacent link of the channel.
    Link *link.Link
}
```

Both the client and server instances needs to manage channels. Channels are associated with a channel ID and also the public key(s) of the remote instances that the channel interacts with. Channels are hence identified by *Link* + *Channel ID*.

From the perspective of a *Client Instance*, the assignment of *Channel IDs* are unique to a given link with a *Server Instance*. For example, let's say client 'A' is connected with server 'B' and server 'C', hence we have links 'AB' and 'AC'. We can have 'AB' and 'AC' share the same Channel ID, but because the channel itself is associated with a different link, they are considered different channels.

From the perspective of a *Server Instance*, the assignment of *Channel IDs* are unique to a given link with a *Client Instance*.

### Opening a Channel

A channel in it's entirety handles the communication between two client instances via a server instance (which acts as a relay). Within the link between a single client instance and the server instance, a channel is represented using a *Channel ID*. The *Channel ID* of the two *links* of the same "channel" can be different, and the *Server Instance* is responsible for recording this association of the *Channel IDs* (coupled with the client instance's public key).

When a *Client Instance* wishes to communicate with another *Client Instance*, it is responsible for initiating the creation of a channel. To do so, t sends a `OpenChannel` frame to the *Server Instance* in which:

- `ChannelID` contains a ChannelID that the client wishes to associate with the channel.
- `RemoteStatic` contains the public key of the remote *Client Instance* that the local client wishes to communicate via this channel.
- `NoiseMessage1` is the first noise handshake message (the handshake pattern used is KK).

If the *Server Instance* wishes to reject the request to open channel, it can send a `ChannelClosed` frame back to the initiating client with the `ChannelID` sub-field containing the value of the channel ID suggested by the initiating client.

If the *Server Instance* wishes to go forward with opening of a channel, it sends a `OpenChannel` frame to the second *Client Instance*, in which `ChannelID` is an ID that's unique between the server and the second client and public key of the first client.

If the second *Client Instance* wishes to reject the request, it can send a `ChannelClosed` frame back to the server, and the server can subsequently send a `ChannelClosed` frame to the initiating client (the `ChannelID` sub-fields of these `ChannelClosed` frames should be the unique channel IDs of the associated links).

If the second *Client Instance* accepts the request, it sends a `ChannelOpened` back to the *Server Instance* (with the `NoiseMessage2`). Subsequently, the *Server Instance* sends a `ChannelOpened` back to the initiating client (the `ChannelID` sub-fields of these `ChannelOpened` frames should be the unique channel IDs of the associated links).

### Closing a Channel

A *Client Instance* can safely close any of it's channels by sending a `CloseChannel` (with the associated `ChannelID`) to the *Server Instance*.

After a *Client Instance* sends a `CloseChannel`, no more frames are to be sent by that instance. However, the remote instance can still send frames until it receives the `CloseChannel` to it. The "close-responding" client then sends a `ChannelClosed` instance back to the "close-initiating" client. Once the `ChannelClosed` channel is sent by the "close-responding" client, it will no longer send or receive frames. Once the "close-initiator" receives the `ChannelClosed` frame. it will no longer receive frames.

In summary,

- When a client instance sends a `CloseChannel` frame, the channel is "partially-closed" and the client instance will only receive and not send via the channel. If a `ChannelClosed` frame is not received after a given timeout, the channel sends a `ChannelClosed` itself and the channel is "fully-closed".
- When a client instance receives a `CloseChannel` frame, it delivers a `ChannelClosed` frame and the channel is "fully-closed" and the client will no longer receive or send via the channel.
- When a client instance receives a `ChannelClosed` frame, the channel is "fully-closed".

### Handling Disconnections

In any given situation, there may be a possibility that the *Server Instance* unexpectedly disconnects with a *Client Instance*, or that a *Client Instance* unexpectedly disconnects with a *Server Instance*. This should directly affect the channels associated with the *Link* in question.

When a *Client Instance* detects that a *Server Instance* has disconnected from it. All associated channels with that *Server Instance* should be closed. When a channel closes, the associated *Transport* should also be closed.

When a *Server Instance* detects a disconnection from a *Client Instance*, it should send a `ChannelClosed` frame to all the other *Client Instances* that shares a channel with the disconnected client. After so, the *Server Instance* should dissociate all relations with the closed channels.
