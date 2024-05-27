# Skywire Specifications

The Skywire specifications.

## Table Of Contents

- [Overview](#overview)
- [HTTP Authorization Middleware](#http-authorization-middleware)
  - [Authorization Procedures](#authorization-procedures)
- [Transport](#transport)
  - [Transport Module](#transport-module)
- [Transport Discovery](#transport-discovery)
  - [Transport Discovery Procedures](#transport-discovery-procedures)
  - [Security Procedures](#security-procedures)
  - [Code Structure](#code-structure)
  - [Database](#database)
  - [Endpoint Definitions](#endpoint-definitions)
    - [GET Incrementing Security Nonce](#get-incrementing-security-nonce)
    - [GET Transport Entry via Transport ID](#get-transport-entry-via-transport-id)
    - [GET Transport(s) via Edge Public Key](#get-transports-via-edge-public-key)
    - [POST Register Transport(s)](#post-register-transports)
    - [POST Status(es)](#post-statuses)
- [Dmsg](#dmsg-system)
  - [Dmsg Modules](#dmsg-system-modules)
  - [Dmsg Procedures](#dmsg-procedures)
  - [Dmsg Discovery](#dmsg-discovery)
    - [Instance Entry](#instance-entry)
    - [Store Interface](#store-interface)
    - [Endpoints](#endpoints)
      - [GET Entry](#get-entry)
      - [POST Entry](#post-entry)
      - [GET Available Servers](#get-available-servers)
    - [Dmsg Discovery Client Library](#dmsg-discovery-client-library)
    - [Dmsg Discovery Integration Tests](#dmsg-discovery-integration-tests)
  - [Dmsg Link](#dmsg-link)
    - [Link Handshake Frames](#link-handshake-frames)
    - [Dmsg Frames](#dmsg-frames)
    - [Noise Implementation in Channels](#noise-implementation-in-channels)
    - [Implementation in Code](#implementation-in-code)
  - [Dmsg Instance](#dmsg-instance)
    - [Configuring an Instance](#configuring-an-instance)
    - [Instance Interaction with Dmsg Discovery](#instance-interaction-with-dmsg-discovery)
    - [Channel Management](#channel-management)
    - [Opening a Channel](#opening-a-channel)
    - [Closing a Channel](#closing-a-channel)
    - [Handling Disconnections](#handling-disconnections)
- [Packets](#packets)
  - [Settlement Packets](#settlement-packets)
  - [Foundational Packets](#foundational-packets)
    - [`0x30 CreateLoop`](#0x30-createloop)
    - [`0x31 LoopCreated`](#0x31-loopcreated)
    - [`0x32 LoopNotCreated`](#0x32-loopnotcreated)
    - [`0x33 ConfirmLoop`](#0x33-confirmloop)
    - [`0x34 LoopConfirmed`](#0x34-loopconfirmed)
    - [`0x35 SecureRIDs`](#0x35-securerids)
    - [`0x36 RIDsSecured`](#0x36-ridssecured)
    - [`0x37 RIDsNotSecured`](#0x37-ridsnotsecured)
    - [`0x38 AddRules`](#0x38-addrules)
    - [`0x39 RulesAdded`](#0x39-rulesadded)
    - [`0x3A RemoveRules`](#0x3a-removerules)
    - [`0x3B RulesRemoved`](#0x3b-rulesremoved)
  - [Data Packets](#data-packets)
  - [Loopback Packets](#loopback-packets)
- [Transport Management](#transport-management)
  - [Transport Manager Procedures](#transport-manager-procedures)
  - [Logging](#logging)
- [Route Finder](#route-finder)
  - [Graph Algorithm](#graph-algorithm)
  - [Routing algorithm](#routing-algorithm)
  - [Code Structure](#code-structure-1)
  - [Database](#database-1)
  - [Endpoint Definitions](#endpoint-definitions-1)
    - [GET Routes available for the defined start and end key](#get-routes-available-for-the-defined-start-and-end-key)
- [Route Setup Process](#route-setup-process)
- [Routing Table](#routing-table)
- [Router](#router)
- [App Server](#app-server)
  - [Loop Encryption](#loop-encryption)
- [Setup Node](#setup-node)
- [App Node](#app-node)
  - [App Node Configuration](#app-node-configuration)
  - [App Node RPC Interface](#app-node-rpc-interface)
    - [Commands](#commands)
  - [Ports Management](#ports-management)
- [Manager Node](#manager-node)
  - [Manager Node REST API (and User Interface)](#manager-node-rest-api-and-user-interface)
- [Glossary](#glossary)

# Overview

Skywire is a decentralized *SDN* (software defined network) which attempts to replace the current internet. The *Skywire Network* is made up of hardware (*Skywire Nodes*) running a *Skywire Visor*. There are currently two types of *Skywire Nodes*; *App Node* or *Skywire Visor* and *Route Setup Node*.

Each instance of a *Skywire Visor* is represented by a unique public key. Connections made to that *Skywire Visor* are encrypted to the recipient public key.

A bidirectional & reusable line of communication between two *Skywire Visors* is called a *Transport*. Each *Transport* is represented by a unique *Transport ID* which is of a certain *Transport Type*, and the two *Skywire Visors* that are connected via the *Transport* are the *Transport Edges* or *Edge Keys*.

*Transport Types* include the following:
* SUDPH (or *Skywire-UDP*) transports use a custom UDP-type protocol, for flexible, fast, low-latency connections
* STCPR (or *Skywire-TCP*) transports use a custom TCP-type protocol, are typically established by the public auto-connect when running a *Public Visor*
* STCP (or *Skywire-TCP*) transports also use a custom TCP-type protocol, but are manually configured in the visor's .json config file
* DMSG transports are mediated by dmsg servers. In a technical sense, dmsg transports actually consist of two TCP-type transports which are seamlessly considered as one.

*Transports* enable direct p2p communication between visors. However, **critical to note** that any dmsg transports or connections will use a *Dmsg Server* as an intermediary in that connection or transport. A few implications of the previous statement must be understood:

* Any visor which permits connections via any transport type other than dmsg will have it's ip address exposed to the connecting visor
* The real ip address of any visor is not exposed when only dmsg transports or connections are allowed (i.e. when running a visor with a so-called dmsghttp-config)
* **Transports can only be used in a route**

A *Route* is unidirectional(**?**) and delivers data units called *Packets*. A *Route* may consist of one or multiple *hops* where each hop is a *Transport*. Two *Routes* of opposite directions make a *Loop* when associated with the given *Ports* at each *Loop Edge*. *Loops* handle the communication between two *Skywire Apps* and is represented via the *Loop's* source and destination node's public keys and the source and destination ports (similar to how TCP/UDP handles ports).

**? Maintainer Review Note: In the current implementation routes seem bidirectional by default**

* A multi-hop route consisting of more than one dmsg transport between visors connected to the same dmsg server may transit the same dmsg server multiple times - because currently the dmsg server public key is not accounted for in the transports.
* A multi-hop route can make it difficult if not impossible to determine what traffic is going where. Traffic correlation becomes even more difficult when a transport is used by more than one client.
* Skywire routing is wholly dependent on the *Route Setup Node*

A *Packet* is prefixed with a *Route ID* which lets a *Skywire Visor* identify how the *Packet* is to be handled (forward, or consume). Every *Skywire Visor* has a *Routing Table* with *Routing Rules* for that particular *Skywire Visor*. *Skywire Packets* are all the same size and are fuzzed to appear as noise, and are encrypted to the recipient public key. In addition, the visor which is currently handling the packet only knows the immediate previous and next steps of the route that packet is traversing and does not know the ultimate source and destination of that packet.

In summary,

- *Transports* are responsible for single-hop communication between two *Skywire Visors* and are bidirectional (this may change later on).
- *Transport Setup Nodes* are permissioned to request remote visors to establish transports.
- *Routes* are responsible for multi-hop communication between two *Skywire Visors* and are ~~unidirectional~~ may be configured bidirectionally or unidirectionally.
- *Route Setup Nodes* are responsible for setting up routes.
- *Loops* are responsible for communication between two *Skywire Apps* and are bidirectional (**Maintainer Review Inquiry: Configured where? Special type of route?**).

There are many ways in which we can implement a *Transport*. Each unique method is called a *Transport Type*.

Initially, we need to implement a MVP in which we assume that there are no malicious nodes in the network and discovery of routes, transports and nodes are to be done in a centralized manner. However, basic authentication and encryption is still required.

# HTTP Authorization Middleware

**Maintainer Review Note: HTTP Authorization Middleware is technically not needed if the services are connected to via dmsg**

Skywire is made up of multiple services and nodes. Some of these services/nodes communicate via restful interfaces, and some of the endpoints require authentication and authorization.

As nodes in the Skywire network are identified via public keys, an appropriate approach to authentication and authorization is via public/private key cryptography. The curve to use is `secp256k1`, and when referenced by the RESTFUL endpoints, it is to be represented as a hexadecimal string format.

These HTTP security middleware features should be implemented within the `/pkg/utils/httpauth` module of the `skywire` repository. This module not only provides server-side logic, but also client-side logic to make interaction with the server-side more streamlined.

## Authorization Procedures

To avoid replay attacks and unauthorized access, each remote entity (represented by it's public key) is assigned an *Security Nonce* by the `httpauth` module. The remote entity is required to sign the *Security Nonce* alongside the request body on every request.

For each successful request, the next expected *Security Nonce* is to increment. The `httpauth` module is to provide an interface named `NonceStorer` to keep an record of "remote entity public key" to "next expected nonce" associations. The following is a proposed structure for `NonceStorer`;

```golang
// NonceStorer stores Incrementing Security Nonces.
type NonceStorer interface {

    // IncrementNonce increments the nonce associated with the specified remote entity.
    // It returns the next expected nonce after it has been incremented and returns error on failure.
    IncrementNonce(ctx context.Context, remotePK cipher.PubKey) (nonce uint64, err error)

    // Nonce obtains the next expected nonce for a given remote entity (represented by public key).
    // It returns error on failure.
    Nonce(ctx context.Context, remotePK cipher.PubKey) (nonce uint64, err error)

    // Count obtains the number of entries stored in the underlying database.
    Count(ctx context.Context) (n int, err error)
}
```

Take note that the only times the next-expected *Security Nonce* (for a given remote entity) is to increment, is when a successful request happens.

Initially (when no successful requests has been processed for a given remote entity), the next expected *Security Nonce* should always be zero. When it is this value, the underlying database for the `NonceStorer` implementation should not need an entry for it.

For every request that requires authentication and authorization in this manner, the structure `httpauth.Server` is to handle it. Specifically, it is to "wrap" the original `http.HandlerFunc` to add additional logic for checking the request. Consequently, the `httpauth.Client` appends the needed additional headers to the request.

The following extra header values are required (`SW` stands for Skywire);

- `SW-Public` - Specifies the public key (hexadecimal string representation) of the Skywire Visor performing this operation.
- `SW-Nonce` - Specifies the incrementing nonce provided by this operation.
- `SW-Sig` - Specifies the of the signature (hexadecimal string representation) of the hash result of the concatenation of the Security Nonce + Body of the request.

The `httpauth.Server` should also provide the `http.HandlerFunc` which obtains the next expected incrementing nonce for a given public key. This is required when a remote entity looses sync. A successful response of this call should look something of the following;

```json
{
    "edge": "<public-key>",
    "next_nonce": 0
}
```

The following is a proposed implementation of `httpauth.Server`;

```golang
package httpauth

// Server provides server-side logic for Skywire-related RESTFUL authorization and authentication.
type Server struct {
    // implementation ...
}

// NewServer creates a new authentication server with the provided NonceStorer.
func NewServer(store NonceStorer) *Server {
    // implementation ...
}

// WrapConfig configures the '(*Server).Wrap' function.
type WrapConfig struct {
    // MaxHTTPBodyLen specifies the max body length that is acceptable.
    // No limit is set if the value is 0.
    MaxHTTPBodyLen  int

    // PubKeyWhitelist specifies the whitelisted public keys.
    // If value is nil, no whitelist rules are set.
    PubKeyWhitelist []cipher.PubKey
}

// Wrap wraps a http.HandlerFunc and adds authentication logic.
// The original http.HandlerFunc is responsible for setting the status code.
// The middleware logic should only increment the security nonce if the status code
// from the original http.HandlerFunc is of 2xx value (representing success).
func (as *Server) Wrap(config *WrapConfig, original http.HandlerFunc) http.HandlerFunc {
    // implementation ...
}

// HandleNextNonce returns a http handler that
func (as *Server) NextNonceHandler(remotePK cipher.PubKey) http.HandlerFunc {
    // implementation ...
}
```

Take note that for the `(*Server).Wrap` function, we will need to define a custom `http.ResponseWriter` to obtain the status code (https://www.reddit.com/r/golang/comments/7p35s4/how_do_i_get_the_response_status_for_my_middleware/).

The `httpauth.Client` implementation is responsible for providing logic for the following actions;

- Keep a local record of the next expected *Security Nonce*.
- Adding security header values to a given request (`http.Request`).

# Transport

A *Transport* represents a bidirectional line of communication between two *Skywire Visors* (or *Transport Edges*) and is the fundamental unit of any *Route*. A Transport is responsible for ensuring accurate delivery of data and providing symmetric encryption between the two nodes communicating.

Each *Transport* is represented as a unique 16 byte (128 bit) UUID value called the *Transport ID* and has a *Transport Type* that identifies a specific implementation of the *Transport*.

A *Transport* has the following information associated with it;

- **Transport ID:** A `uuid.UUID` value that uniquely identifies the Transport.
- **Edges:** The public keys of the Transport's edge nodes (should only have 2 edges and the initiating edge should come first).
- **Type:** A `string` value that specifies the particular implementation of the *Transport*.
- **Public:** A `bool` that specifies whether the *Transport* is to be registered in the *Transport Discovery* or not. Only public transports are registered.(**?**)
- **Registered:** A `int64` value that is the epoch time of when the *Transport* is registered in *Transport Discovery*. A value of `0` represents the state where the *Transport* is not (or not yet) registered in the *Transport Discovery*. (**??**)

This is a JSON representation of a *Transport Entry*;

```json
{
    "t_id": "e1808c316b23d1d6119cad1795238ff0",
    "edges": ["031d796272349d597d6d3130497ccd11cf8af12c7d186b1726358abfb49edad0c1", "03bd9724f335c5eb5a1011e7862d4af28488102c8edffc84585cf0826ac4864b38"],
    "type": "dmsg",
    "public": true
}
```

**? Maintainer Review Note: In the current implementation the public field is not included - all transports are registered with transport discovery**
**?? Maintainer Review Note: In the current implementation the time a transport was registered is not publicly displayed in the transport discovery**


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

# Transport Discovery

The Transport Discovery is a service that exposes a RESTful interface and interacts with a database on the back-end.

The database stores *Transport Entries* that can be queried using their *Transport ID* or via a given *Transport Edge*.

The process of submitting a *Transport Entry* is called *Registration* and a Transport cannot be deregistered. However, nodes that are an *Edge* of a *Transport*, can update their *Transport Status*, and specify whether the *Transport* is up or down.

Any state-altering RESTful call to the *Transport Discovery* is authenticated using signatures, and replay attacks are avoided by expecting an incrementing security nonce (all communication should be encrypted with HTTPS anyhow).

## Transport Discovery Procedures

This is a summary of the procedures that the *Transport Discovery* is to handle.

**Registering a Transport:**

Technically, *Transports* are created by the Skywire Visors themselves via an internal *Transport Factory* implementation. The *Transport Discovery* is only responsible for registering *Transports* in the form of a *Transport Entry*.

When two Skywire Visors establish a Transport connection between them, it is at first, unregistered in the *Transport Discovery*. The node that initiated the creation of the Transport (or the node that called the `(transport.Transport).Dial` method), is the node that is responsible for initiating the *Transport Settlement Handshake*.

If two nodes; **A** and **B** establish a *Transport* between them (where **A** is the *Transport Initiator*), **A** is then also responsible for sending the first handshake packet for the *Transport Settlement Handshake*. The procedure is as follows:

1. **A** sends **B** a proposed `transport.Entry` and also **A**'s signature of the Entry (in the form of `transport.SignedEntry`).

2. **B** checks the `transport.SignedEntry` sent from **A**;

   1. The `Entry.ID` field should be unique (check via *Transport Discovery*).
   2. The `Entry.Edges` field should be ordered correctly and contain public keys of **A** and **B**.
   3. The `Entry.Type` field should have the expected Transport Type.
   4. The `Signatures` field should contain **A**'s valid signature in the correct location (in the same index as **A**'s public key in `Entry.Edges`).
   5. The `Registered` field should be empty.

3. **B** then adds it's only signature to the `transport.SignedEntry` and registers it to the *Transport Discovery*. Both public and private Transports are registered in the *Transport Discovery* (however only public *Transports* are publicly available).

4. **B** then informs **A** on the success/failure of the registration, or just that the `transport.SignedEntry` is accepted by itself (depending on whether the Transport is to be public or not).

**Submitting Transport Statuses:**

If a given *Transport* is public, the associated *Transport Edges* is responsible for submitting their individual *Transport Statuses* to the *Transport Discovery* whenever the follow events occur;

- Directly after a *Transport* is first successfully registered in the *Transport Discovery*.
- Whenever the *Transport* comes online/offline (connected/disconnected).

**Maintainer Review Note:**

 In the current implementation, transports are not expired as they should be. Transport bandwidth `day files` are collected hourly in order to measure the bandwidth of a transport. A change is proposed such that the transport discovery will collect the transport bandwidth metrics directly via the aforementioned reporting system. Additionally, a re-registration interval and a mechanism for expiring transports which have not been re-registered within that interval from the transport discovery are also needed. Or any other solution to address the issue of concurrency between transport discovery and the transports a visor may have registered locally.

**Obtaining Transports:**

There are two ways to obtain transports; either via the assigned *Transport ID*, or via one of the *Transport Edges*. There is no restriction as who can access this information and results can be sorted by a given meta.

## Security Procedures

**Incrementing Security Nonce:**

An *Incrementing Security Nonce* is represented by a `uint64` value.

To avoid replay attacks and unauthorized access, each public key of a *Skywire Visor* is assigned an *Incrementing Security Nonce*, and is expected to sign it with the rest of the body, and include the signature result in the http header. The *Incrementing Security Nonce* should increment every time an" endpoint is called (except for the endpoint that obtains the next expected incrementing security nonce). An *Incrementing Security Nonce* is not operation-specific, and increments every time any endpoint is called by the given Skywire Visor.

The *Transport Discovery* should store a table of expected next *Incrementing Security Nonce* for each public key of a *Skywire Visor*. There is an endpoint `GET /security/nonces/{public-key}` that provides the next expected *Incrementing Security Nonce* for a given Visor public key. This endpoint should be publicly accessible, but nevertheless, the *Skywire Visors* themselves should keep a copy of their next expected *Incrementing Security Nonce*.

The only times an *Incrementing Security Nonce* should not increment is when:

- An invalid request is submitted (missing/extra fields, invalid signature).
- An internal server error occurs.

Initially, the expected *Incrementing Security Nonce* should be 0. When it is this value, the *Transport Discovery* should not have an entry for it.

Each operation should contain the following extra header entries:

- `SW-Public` - Specifies the public key of the Skywire Visor performing this operation.
- `SW-Nonce` - Specifies the incrementing nonce provided by this operation.
- `SW-Sig` - Specifies the hex-representation of the signature of the hash result of the concatenation of the *Incrementing Security Nonce* + Body of the request.

If these values are not valid, the *Transport Discovery* should reject the request.

## Code Structure

The code should be in the `skywire-services` repository.

- `/cmd/transport-discovery/transport-discovery.go` is the main executable for the *Transport Discovery*.
- `/pkg/transport-discovery/api/` contains the RESTFUL API definitions.
- `/pkg/transport-discovery/store/` contains the definition of the `Storer` interface and it's implementations.
- `/pkg/transport-discovery/client/` contains the client library that interacts with the *Transport Discovery* server's RESTFUL API.

## Database

The *Transport Discovery* should work with a variety of databases and the following interfaces should be defined for such implementations;

- `TransportStorer` should store *Transport Signed Entries* and it's associated *Transport Statuses*.
- `NonceStorer` should store expected *Incrementing Nonces*.

## Endpoint Definitions

The following is a summary of all the *Transport Discovery* endpoints.

- `GET /security/nonces/edge:<public-key>`
- `GET /transports/id:<transport-id>`
- `GET /transports/edge:<public-key>`
- `POST /transports`
- `POST /statuses`

All endpoints should include an `Accept: application/json` field and the response header should include an `Content-Type: application/json` field.

All requests (except for obtaining the next expected incrementing nonce) should include the following fields.

```
Accept: application/json
Content-Type: application/json
SW-Public: <public-key>
SW-Nonce: <nonce>
SW-Sig: <signature>
```

### GET Incrementing Security Nonce

Obtains the next expected incrementing nonce for a given edge's public key.

**Request:**

```
GET /security/nonces/<public-key>
```

**Responses:**

- 200 OK (Success).
    ```json
    {
        "edge": "<public-key>",
        "next_nonce": 0
    }
    ```
- 400 Bad Request (Malformed request).
- 500 Internal Server Error (Server error).

### GET Transport Entry via Transport ID

Obtains a *Transport* via a given *Transport ID*.

Should only return a single `"transport"` result.

**Request:**

```
GET /transports/id:<transport-id>
```

**Responses:**

- 200 OK (Success).
    ```json
    {
        "entry": {
            "id": "<transport-id>",
            "edges": [
                "<public-key-1>",
                "<public-key-2>"
            ],
            "type": "<transport-type>"
        },
        "is_up": true,
        "registered": 0
    }
    ```
- 400 Bad Request (Malformed request).
- 500 Internal Server Error (Server error).

### GET Transport(s) via Edge Public Key

Obtains *Transport(s)* via a given *Transport Edge* public key.

**Request:**

```
GET /transports/edge:<public-key>
```

**Responses:**

- 200 OK (Success).
    ```json
    [
        {
            "entry": {
                "t_id": "<transport-id-1>",
                "edges": [
                    "<public-key-1>",
                    "<public-key-2>"
                ],
                "type": "<transport-type>",
                "public": true
            },
            "is_up": true,
            "registered": 0
        },
        {
            "entry": {
                "t_id": "<transport-id-2>",
                "edges": [
                    "<public-key-1>",
                    "<public-key-2>"
                ],
                "type": "<transport-type>",
                "public": true
            },
            "is_up": false,
            "registered": 0
        }
    ]
    ```
- 400 Bad Request (Malformed request).
- 500 Internal Server Error (Server error).

### POST Register Transport(s)

Registers one or multiple Transports.

**Request:**

```
POST /transports
```

```json
[
    {
        "entry": {
            "id": "<transport-id-1>",
            "edges": [
                "<public-key-1>",
                "<public-key-2>"
            ],
            "type": "<transport-type-1>",
            "public": true
        },
        "signatures": [
            "<signature-1>",
            "<signature-2>"
        ]
    },
    {
        "entry": {
            "id": "<transport-id-2>",
            "edges": [
                "<public-key-1>",
                "<public-key-3>"
            ],
            "type": "<transport-type-2>",
            "public": true
        },
        "signatures": [
            "<signature-1>",
            "<signature-3>"
        ]
    }
]    
```

**Responses:**

- 200 OK (Success).
    ```json
    [
        {
            "entry": {
                "id": "<transport-id-1>",
                "edges": [
                    "<public-key-1>",
                    "<public-key-2>"
                ],
                "type": "<transport-type-1>",
                "public": true
            },
            "signatures": [
                "<signature-1>",
                "<signature-2>"
            ],
            "registered": 0
        },
        {
            "entry": {
                "id": "<transport-id-2>",
                "edges": [
                    "<public-key-1>",
                    "<public-key-3>"
                ],
                "type": "<transport-type-2>",
                "public": true
            },
            "signatures": [
                "<signature-1>",
                "<signature-3>"
            ],
            "registered": 0
        }
    ]
    ```
- 400 Bad Request (Malformed request).
- 401 Unauthorized (Invalid signature/nonce).
- 408 Request Timeout (Timed out).
- 500 Internal Server Error (Server error).

### POST Status(es)

Submits one or multiple *Transport Status(es)* from the perspective of the submitting node. The returned result is the final *Transport Status(es)* determined by the *Transport Discovery* that is generated using the submitted *Transport Status(es)* of the two edges.

When a Transport is registered, it is considered to be *up*. Then after, every time a node's *Status* is submitted, the *Transport Discovery* alters the final state *Status* with the following rules:

- If there is only one edge's *Status* submitted, the final status is of that of the submitted *Status*.
- If there are two *Status*es submitted and they both agree, final *Status* will also be the same.
- If the two submitted *Status*es disagree, then the final *Status* is always *Down*.

**Request:**

```
POST /statuses
```

```json
[
    {
        "id": "<transport-id-1>",
        "is_up": true
    },
    {
        "id": "<transport-id-2>",
        "is_up": true
    }
]
```

**Responses:**

- 200 OK (Success).
    ```json
    [
        {
            "id": "<transport-id-1>",
            "is_up": true,
            "updated": 0
        },
        {
            "id": "<transport-id-2>",
            "is_up": false,
            "updated": 0
        }
    ]
    ```
- 400 Bad Request (Malformed request).
- 401 Unauthorized (Invalid signature/nonce).
- 408 Request Timeout (Timed out).
- 500 Internal Server Error (Server error).

# Dmsg

[Dmsg](https://github.com/skycoin/dmsg) is an initial implementation of the `Transport` and associated interfaces. To work, dmsg requires an active internet connection and is designed to be horizontally scalable.

Three services make up dmsg: *Dmsg Client* (or *Client Instance*), *Dmsg Server* (or *Service Instance*) and *Dmsg Discovery*.

*Dmsg Clients* and *Dmsg Servers* are represented by public/private key pairs. *Dmsg Clients* deliver data to one another via *Dmsg Servers* which act as relays.

The *Dmsg Discovery* is responsible for allowing *Dmsg Clients* to find other advertised *Dmsg Clients* via their public keys. It is also responsible for finding appropriate *Dmsg Servers* that either "advertise" them, or "advertise" other *Dmsg Clients*.

```
           [D]

     S(1)        S(2)
   //   \\      //   \\
  //     \\    //     \\
 C(A)    C(B) C(C)    C(D)
```

Legend:
- ` [D]` - Discovery Service
- `S(X)` - Dmsg Server (Server Instance)
- `C(X)` - Dmsg Client (Client Instance)

## Dmsg Modules

There are two modules of dmsg.

- `dmsg-discovery` contains the implementation of the *Dmsg Discovery*.
- `dmsg` contains the implementation of either a *Client Instance* or a *Server Instance* of a *Dmsg Client* or *Dmsg Server*.

## Dmsg Procedures

This is a summary of the procedures that the *Dmsg* is to handle.

**Advertising a client:**

To be discoverable by other clients, a client needs to advertise itself.

1. Client queries the Discovery to find available Servers.
2. Client connects to some (or all) of the suggested Servers.
3. Client updates it's own record in Discovery to include it's delegated Servers.

**Client creates a channel to another client:**

In order for two clients to communicate, both clients need to be connected to the same dmsg server, and create a channel to each other via the server.

1. Client queries discovery of the remote client's connected servers. The client will connect to one of these servers if it originally has no shared servers with the remote.
2. The client sends a `OpenChannel` frame to the remote client via the shared server.
3. If the remote client accepts, a `ChannelOpened` frame is sent back to the initiating client (also via the shared server). A channel is represented via two *Channel IDs* (between the initiating client and the server, and between the responding client and the server). The associated between the two channel IDs is defined within the server.
4. Once a channel is created, clients can communicate via one another via the channel.

## Dmsg Discovery

The *Dmsg Discovery* acts like a DNS for dmsg instances (*Dmsg Clients* or *Dmsg Servers*).

### Instance Entry

An entry within the *Dmsg Discovery* can either represent a *Dmsg Server* or a *Dmsg Client*. The *Dmsg Discovery* is a key-value store, in which entries (of either server or client) use their public keys as their "key".

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

    // Contains the node's required client meta if it's to be advertised as a Dmsg Client.
    Client *Client `json:"client,omitempty"`

    // Contains the node's required server meta if it's to be advertised as a Dmsg Server.
    Server *Server `json:"server,omitempty"`

    // Signature for proving authenticity of of the Entry.
    Signature string `json:"signature,omitempty"`
}

// Client contains the node's required client meta, if it is to be advertised as a Dmsg Client.
type Client struct {
    // DelegatedServers contains a list of delegated servers represented by their public keys.
    DelegatedServers []string `json:"delegated_servers"`
}

// Server contains the node's required server meta, if it is to be advertised as a Dmsg Server.
type Server struct {
    // IPv4 or IPv6 public address of the Dmsg Server.
    Address string `json:"address"`

    // Port in which the Dmsg Server is listening for connections.
    Port string `json:"port"`

    // Number of connections still available.
    AvailableConnections int `json:"available_connections"`
}
```

**Definition rules:**

- A record **MUST** have either a "Server" field, a "Client" field, or both "Server" and "Client" fields. In other words, a dmsg public key can be a Dmsg Server, a Dmsg Client, or both a Dmsg Server and a Dmsg Client.

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

The underlying database of the *Dmsg Discovery* is a key-value store. The `Store` interface allows many databases to be used with the *Dmsg Discovery*.

```golang
type Store interface {
    // Entry obtains a single dmsg instance entry.
    // 'static' is a hex representation of the public key identifying the dmsg instance.
    Entry(ctx context.Context, static string) (*Entry, error)

    // SetEntry set's an entry.
    // This is unsafe and does not check signature.
    SetEntry(ctx context.Context, entry *Entry) error

    // AvailableServers discovers available dmsg servers.
    // Obtains at most 'maxCount' amount of available servers obtained randomly.  
    AvailableServers(ctx context.Context, maxCount int) ([]*Entry, error)
}
```

### Endpoints

Only 3 endpoints need to be defined; Get Entry, Post Entry, and Get Available Servers.

#### GET Entry
Obtains a dmsg node's entry.
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

### Dmsg Discovery Client Library

The module is named `client`. It contains a `HTTPClient` structure, that defines how the client will interact with the *Dmsg Discovery* API.

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

### Dmsg Discovery Integration Tests

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

## Dmsg Link

The `link` provides two *Dmsg Instances* a means to establish a connection with one another, and also handle a pool of connections.

Using the *Dmsg Discovery*, a *Dmsg Instance* can discover other instances via only their public key. However, a *Link* requires both a public key and an address:port.

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
- `"version"` specifies the version of dmsg protocol that the initiator is using (`"0.1"` for now).
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

### Dmsg Frames

After the handshake phase, frames have a reoccurring format. These are the *Dmsg Frames* of dmsg.

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

Within the `dmsg` module:

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
    // Public determines whether the instance is to advertise itself to the dmsg discovery servers.
    Public bool

    // DiscoveryAddresses contains the dmsg discovery services to be used (in order of preference).
    DiscoveryAddresses []string
}
```

The above structure is to be an input when creating a *Server Instance* or a *Client Instance*.

### Instance Interaction with Dmsg Discovery

On startup `Server` that is supposed to be publicly available should register itself in dmsg discovery. To do so it first has to fetch current version of an `Entry`, if entry doesn't exist it should create one. If entry exists it may update it if necessary.

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

# Packets

The *Node Module* handles data encapsulated within data units called *Packets*. *Packets* can be grouped within the following categories based on their use-case;

- ***Settlement Packets*** are used by the *Transport Manager* to "settle" Transports. Settlement, allows the two nodes that are the edges of the transport to decide on the *Transport ID* to be used, and whether the Transport is to be public. Only after a *Transport* is settled, can the *Router* have access to the Transport.

    *Settlement Packets* contain `json` encoded payload.

- ***Foundational Packets*** are used by a *Router* to communicate with a remote *Route Setup Node* and is used for setting up, establishing and destroying routes.

    *Foundational Packets* are prefixed by 3 bytes: the packet size (2 bytes) and a Type (1 byte) that contains the foundational packet type.

- ***Data Packets*** are Packets that are actually used to encapsulate data delivered between two Apps.

    *Data Packets* are prefixed by 6 bytes; including the packet size (2 bytes) and the Route ID (4 bytes) which can have any value other than `0x00` or `0x01`.

- ***Loopback Packets*** are packets that are consumed locally by the node.

    *Loopback Packets* are structurally similar to data packets but their Route ID links to a rule that specifies which app to forward the packet to.

## Settlement Packets

After a Transport is established between two nodes, the nodes needs to decide on the *Transport ID* that describes the Transport and whether the Transport is to be public or private (public Transports are to be registered in the *Transport Discovery*). This process is called the *Settlement Handshake*.

The Packets of this handshake contain `json` encoded messages.

*Settlement Handshake* packets do not need a field for Packet-type are they are expected in a specific order.

- Request to settle transport is sent by the *Transport Initiator* to the *Transport Responder* after a *Transport* connection is established.

    JSON Body: Contains a `transport.SignedEntry` structure with the *Transport Initiator*'s signature.

- *Transport Responder* should validate submitted `transport.SignedEntry`, and if entry is valid it should add sign it and perform transport registration in transport discovery. If registration was successful responder should send updated `transport.SignedEntry` back to initiator.

    JSON Body: Contains a `transport.SignedEntry` structure with signatures from both the *Transport Initiator* and the *Transport Responder*. If the transport is registered in *Transport Discovery*, the `SignedTransport.Registered` should contain the epoch time of registration.

If transport will fail at any step participants can chose to stop handshake procedures and close corresponding transport. Transport disconnect during the handshake should be handled appropriately by participants. Optional handshake timeout should also be supported.

## Foundational Packets

Foundational packets are used for the communication between the *Skywire Visor* and *Route Setup Nodes*.

The *Route Setup Node* is responsible for fulfilling Route initiating and destroying requests by communicating with the initiating, responding and intermediate nodes of the proposed route.

The following is the expected format of a Foundational Packet;

```
| Packet Len | Type   | JSON Body |
| 2 bytes    | 1 byte | ~         |
```

- ***Packet Len*** specifies the total packet length in bytes (exclusive of the *Packet Len* field).
- ***Type*** specifies the *Foundational Packet Type*.
- ***JSON Body*** is the packet body (in JSON format) that is unique depending on the packet type.

**Foundational Packet Types Summary:**

| Type | Name |
| ---- | ---- |
| 0x00 | `AddRules` |
| 0x01 | `RemoveRules` |
| 0x02 | `CreateLoop` |
| 0x03 | `ConfirmLoop` |
| 0x04 | `CloseLoop` |
| 0x05 | `LoopClosed` |
| 0xfe | `ResponseFailure` |
| 0xff | `ResponseSuccess` |

### `0x00 AddRules`

Sent by the *Route Setup Node* to all *Nodes* of the route. This packet informs nodes what rules are to be added to their internal routing table.

**JSON Body:**

```json
[<rule-1>, <rule-2>]
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with
    ```json
    [<rid-1>, <rid-2>]
    ```

### `0x01 RemoveRules`

Sent by the *Route Setup Node* to *Visor* of the route.

**JSON Body:**

```json
["<rid-1>", "rid-2"]
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with
    ```json
    [<rid-1>, <rid-2>]
    ```

### `0x02 CreateLoop`

Sent by the *Route Initiator* to a *Route Setup Node* to have a *Loop* created.

**JSON Body:**

```json
{
    "local-port": <local-port>,
    "remote-port": <remote-port>,
    "forward": [
        {
            "from": "<pk-1>",
            "to": "<pk-2>",
            "tid": "<tid-1>"
        },
        {
            "from": "<pk-2>",
            "to": "<pk-3>",
            "tid": "<tid-2>"
        }
    ],
    "reverse": [
        {
            "from": "<pk-3>",
            "to": "<pk-2>",
            "tid": "<tid-2>"
        },
        {
            "from": "<pk-2>",
            "to": "<pk-1>",
            "tid": "<tid-1>"
        }
    ],
    "expiry": "<expiry>"
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty payload.

### `0x3 ConfirmLoop`

Sent by the *Route Setup Node* to Responder and Initiator *Visor* to confirm notify about route in opposite direction.

**JSON Body:**

```json
{
    "remote-pk": "<pk>",
    "remote-port": <remote-port>,
    "local-port": <local-port>,
    "resp-rid": <resp-rid>
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty payload.

### `0x4 CloseLoop`

Sent by a Responder or Initiator *Visor* to a *Route Setup Node* to notify about closing a loop locally.

**JSON Body:**

```json
{
    "port": "<local-port>",
    "remote": {
        "port": <remote-port>,
        "pk": <remote-pk>
    }
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty payload.

### `0x5 LoopClosed`

Sent by a *Route Setup Node* to a Responder or Initiator to notify about closed loop on the opposite end.

**JSON Body:**

```json
{
    "port": "<local-port>",
    "remote": {
        "port": <remote-port>,
        "pk": <remote-pk>
    }
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty payload.


## Data Packets

The follow is the structure of a *Data Packet*.

```
| Packet Len | Route ID | Payload |
| 2 bytes    | 4 bytes  | ~       |
```

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

# Route Finder

The *Route Finder* (or *Route Finding Service*) is responsible for finding and suggesting routes between two Skywire Nodes (identified by public keys). It is expected that an *App Node* is to use this service to find possible routes before contacting the *Route Setup Node*.

In the initial version of the *Route Finder*, it should use a basic algorithm to choose and order the best routes. This algorithm should find the x amount (limited by the max routes parameter) of "fastest" routes determined by the least amount of hops needed, and order it by hops ascending.

The implementation of *Route Finder* requires only a single REST API endpoint.

## Graph Algorithm

In order to explore routing we need to create a graph that represents the current skywire network, or at least the network formed by all the reachable nodes by `source node`.

For this purpose, we use the `mark and sweep` algorithm. Such an algorithm consists of two phases.

In the first phase, every object in the graph is explored in a `Deep First Search` order. This means that we need to explore every transport starting from route node, accessing the `transport-discovery` database each time that we need to retrieve new information.

In the second phase, we remove nodes from the graph that have not been visited, and then mark every node as unvisited in preparation for the next iteration of the algorithm.

An explanation and implementation of this algorithm can be found [here](https://www.geeksforgeeks.org/mark-and-sweep-garbage-collection-algorithm/).

## Routing algorithm

Given the previous graph we can now explore it to find the best routes from every given starting node to destiny node.

For this purpose we use a modification of `Dijkstra algorithm`.

An implementation can be found [here](http://rosettacode.org/wiki/Dijkstra%27s_algorithm#Go).

Route-finder modifies this algorithm by keeping track of all the nodes that reached to destination node. This allows the ability to backtrack every best route that arrives from a different node to destination node.

## Code Structure

The code should be in the `skycoin/skywire` repository;

- `/cmd/route-finder/route-finder.go` is the main executable for the *Route Finder*.
- `/pkg/route-finder/api/` contains the RESTFUL API definitions.
- `/pkg/route-finder/store/` contains the definition of the `Storer` interface and it's implementations [**TODO**].
- `/pkg/route-finder/client/` contains the client library that interacts with the *Route Finder* service's RESTFUL API.

## Database

The *Route Finder* only accesses the Transport database already defined in the *Transport Discovery* specification.

## Endpoint Definitions

All endpoint calls should include an `Accept: application/json` field in the request header, and the response header should include an `Content-Type: application/json` field.

### GET Routes available for the defined start and end key

Obtains the routes available for a specific start and end public key. Optionally with custom min and max hop parameters.

Note that each transport is to be represented by the `transport.Entry` structure.

**Request:**

```
GET /routes
```

```json
{
    "src_pk": "<src-pk>",
    "dst_pk": "<dst-pk>",
    "min_hops": 0,
    "max_hops": 0,
}
```

**Responses:**

- 200 OK (Success).
    ```json
    {
        "routes": [
            {
                "transports": [
                    {
                        "tid": "<tid>",
                        "edges": ["<initiator-pk>", "<responder-pk>"],
                        "type": "<type>",
                        "public": true,
                    }
                ]
            }
        ]
    }
    ```
- 400 Bad Request (Malformed request).
- 500 Internal Server Error (Server error).

# Route Setup Process

1. Route paths are uni-directional. So, the whole route between 2 visors consists of forward and reverse paths. *Setup node* receives both of these paths in the routes setup request.
2. For each node along both paths *Setup node* calculates how many rules are to be applied.
3. *Setup node* connects to all the node along both paths and sends `ReserveIDs` request to reserve available rule IDs needed to setup the route.
4. *Setup node* creates rules the following way. Let's consider visor A setting up route to visor B. This way we have forward path `A->B` and reverse path `B->A`. For forward path we create `Forward` rule for visor `A`, `IntermediaryForward` rules for each node between `A` and `B`, and `Consume` rule for `B`. For reverse path we create `Forward` rule for visor `B`, `IntermediaryForward` rules for each visor between `B` and `A`, and `Consume` rule for `A`.
5. *Setup node* sends all the created `IntermediaryForward` rules to corresponding visors to be applied.
6. *Setup node* sends `Consume` and `Forward` rules to visor `B` (remote in our case).
7. *Setup node* sends `Forward` and `Consume` rules to visor `A` in response to the route setup request.

## Loop Setup Process:

In the code `Loop` is represented by the following structure:

```golang
// Hop defines a route hop between 2 nodes.
type Hop struct {
    From      cipher.PubKey // Sender's pk
	To        cipher.PubKey // Receiver's pk
	Transport uuid.UUID     // Transport ID between sender and receiver
}

// Route defines a route as a set of Hops.
type Route []*Hop

// Loop defines a loop over a pair of routes.
type Loop struct {
	LocalPort  uint16    // Initiator's port
	RemotePort uint16    // Responder's port
	Forward    Route     // Initial route
	Reverse    Route     // Route in opposite direction
	ExpireAt   time.Time // Expiration time
}

```

Setup procedures:

1. *Initiating Node* sends `CreateLoop` command to a *Route Setup Node*.
2. *Route Setup Node* contacts all nodes along a `Reverse` route using a separate dmsg channel and setups routing rules using `AddRules` command.
3. *Route Setup Node* performs the same operation for a `Forward` route.
4. *Route Setup Node* sends `ConfirmLoop` to initiator with reverse id for a `Forward` route.
5. *Route Setup Node* sends `ConfirmLoop` to responder with reverse id for a `Reverse` route.

If at any point *Route Setup Node* will be unable to proceed it will issue requests to remove rules for the loop.

## Loop Close Process:

Loop can be closed by edge nodes at any time by sending `CloseLoop` command to a *Route Setup Node*.

Close procedures:

1. *Visor* sends `CloseLoop` command to a *Route Setup Node*.
2. *Route Setup Node* sends `LoopClosed` to an opposite *Visor*.
3. Forward routes are not removed since it's not possible to reconstruct the route. They will be removed by expiration timeout.

# Routing Table

A *Routing Table* (located within the `skywire/pkg/node` module) is unique for a given Node's public key. It is basically a key-value store in which the key is the *Route ID* and the value is the *Routing Rule* for the given *Route ID*.

Initially, there will be two types of Routing Rules: *App* and *Forward*.

- *App* rules are identified by their unique `<r-type>` value of `0x00`. A packet which contains a *Route ID* that associates with a *App* rule is to be sent to a local App.
- *Forward* rules are identified by their unique `<r-type>` value of `0x01`. A packet which contains a *Route ID* that associates with a *Forward* rule is to be forwarded.

| Action | Key (Route ID) | Value (Routing Rule) |
| ------ | -------------- | -------------------- |
| *App* | `<rid>`<br>*4 bytes* | `<expiry><r-type><resp-rid><loop-data>`<br>*48 bytes* |
| *Forward* | `<rid>`<br>*4 bytes* | `<expiry><r-type><next-rid><next-tid>`<br>*29 bytes* |

- `<rid>` is the *Route ID* `uint32` key (represented by 4 bytes) that is used to obtain the routing rules for the Packet.
- `<expiry>` contains the epoch time (8 bytes) of when the rule is to be discarded (or becomes invalid).
- `<r-type>` specifies the type of Routing Rule (1 byte). Currently there are two possible routing rule types; *App* (`0x00`) and *Forward* (`0x01`).
- `<resp-rid>` is the *Route ID* (4 bytes) that is the Route ID key for the reserve Route of the loop.
- `<loop-data>` identifies and classifies the loop. It contains the following sub-fields; `<remote-pk><remote-port><local-port>`.
    - `<remote-pk>` is the remote edge public key in which this route/loop is associated with. It is represented by 33 bytes.
    - `<remote-port>` is the remote port in which this route/loop is associated with. It is represented by 2 bytes.
    - `<local-port>` is the local port in which this route/loop is associated with. It is represented by 2 bytes.
- `<next-rid>` is the *Route ID* that is to replace the `<rid>` before the Packet is to be forwarded.
- `<next-tid>` represents the transport which the packet is to be forwarded to. A Transport ID is 16 bytes long.

Every time a Skywire Node receives a packet, it performs the following steps:

1. Obtain the `<rid>` from the Packet, and uses this value to obtain a routing rule entry from the routing table. If no routing rule is found, or the routing rule has already expired (via checking the `<expiry>` field), the Packet is then discarded.
2. Obtains the `<r-type>` value to determine how the packet is to be dealt with. If the `<r-type>` value is `0x00`, the packet is then to be sent to the local *App Server* with the Routing Rule. If the `<r-type>` value is `0x01`, the packet is to be forwarded; continue on to step 3.
3. Obtain the `<next-rid>` from the *Routing Rule* and replace the `<rid>` from the *Route ID* field of the Packet.
4. Forward the Packet to the referenced transport specified within `<next-tid>`.

The routing table is to be an interface.

```golang
package node

// RangeFunc is used by RangeRules to iterate over rules.
type RangeFunc func(routeID transport.RouteID, rule RoutingRule) (next bool)

// RoutingTable represents a routing table implementation.
type RoutingTable interface {
	// AddRule adds a new RoutingRules to the table and returns assigned RouteID.
	AddRule(rule RoutingRule) (routeID transport.RouteID, err error)

	// SetRule sets RoutingRule for a given RouteID.
	SetRule(routeID transport.RouteID, rule RoutingRule) error

	// Rule returns RoutingRule with a given RouteID.
	Rule(routeID transport.RouteID) (RoutingRule, error)

	// DeleteRules removes RoutingRules with a given a RouteIDs.
	DeleteRules(routeIDs ...transport.RouteID) error

	// RangeRules iterates over all rules and yields values to the rangeFunc until `next` is false.
	RangeRules(rangeFunc RangeFunc) error

	// Count returns the number of RoutingRule entries stored.
	Count() int
}
```

Potential improvement we could consider is to move ports from the rules into the data packet header, aligning this with `tcp`. By doing so we will be able to re-use intermediate forward rules across multiple loops which can drastically improve loop establishment time for complex loops.

# Router

The `Router` (located in the `skywire/pkg/router` package) uses the *Transport Manager* and the *Routing Table* internally, and is responsible for the handling incoming Packets (either from external nodes via transports, or internally via the `AppServer`), and also the process for setting up routes.

Regarding the Route setup process, a router should be able to interact with multiple trusted *Route Setup Nodes*.

Every transport created or accepted by the *Transport Manager* should be handled by the *Router*. All incoming packets should be cross-referenced against *Routing Table* and either be forwarded to next *Visor* or to a local *App*.

*Transport Manager* is also responsible for managing connections on local ports for node's apps. *App Node* will request new local connection from the *Router* on *App* startup. All incoming packets from the app's connections should be forwarded based on *App* rules defined in a local routing table. *Transport Manager* should also be capable of requesting new loops from a *Route Setup Node*.

## Port management

Router is responsible for port management. Port allocation algorithm should work similarly to how `tcp` manages ports:

- Certain range of ports should be reserved and be inaccessible for general purpose apps. Default staring port is `10`.
- All allocated local ports should be unique.
- App should be able to allocate static ports that it will be accessible on for remote connections. Static port allocation is performed during `app` init.
- App should be able to dynamically allocate local port for newly created loops.
- Allocated ports should be closed on app shutdown or disconnect from the node.

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

# Setup Node

The *Route Setup Node* (located within the `skywire/pkg/node` module) uses *Transport Manager* internally and is responsible for propagation of routing rules to nodes along the *Route*. *Route Setup Node* should be only addressed by a public key and should work over dmsg transport using multiple channels. Each channel can be used to issue route setup commands by initiator. For *Loop* setup requests *Visor* will be an initiator, for *Rule* setup related operations *Route Setup Node* will be a channel initiator. *Route Setup Node* is only responsible for handling *Foundational Packets* and doesn't perform any forwarding functions.

# App Node

An App Node is a node that is part of the Skywire network and is represented by a key pair (using the `secp256k1` curve). It handles Transports to remote nodes, sets up routes and loops (via Routing Rules and interaction with the *Route Setup Node*), and manages Apps.

Each App is it's own executable that communicates with an *App Node* using a pair of *POSIX* pipes. Piped connection is setup on *App* startup and inherited by a forked *App* process using file descriptor `3` and `4`. Setup process for a forked *App* is handled by the `app` package.

```
    [Skywire Node]
    /      |     \
   /       |      \
[App 1] [App 2] [App 3]
```

## Communication reliability

Since dmsg and loop communication is dependent on intermediate servers we have to provide acknowledgment mechanism between edge nodes. This will be done via wrapper `io.ReadWriter` (`AckReadWriter`) that will be able to augment existing communication channels with `ack` packets. `Write` calls on `AckReadWrite` should block until corresponding `ack` packet is received.

`ack` logic should be working in `tcp`-like way: all pending `ack` packets should be sent with subsequent write on the opposite edge. If no write is happened within a certain interval then all pending `ack` packets should be flushed. Outstanding `ack` packets should also be flushed on `Close` call.

`AckReadWriter` should be able to send and receive 2 types of packets: `payload`(`0x0`) and `ack` (`0x1`):

Format of a `payload` packet:

```
| Packet Type | Packet ID | Payload |
| 0x0         | 1 byte    | ~       |
```

Format of an `ack` packet:

```
| Packet Type | Packet ID | SHA256   |
| 0x1         | 1 byte    | 32 bytes |
```

`AckReadWriter` should be able to prepend any amount of `ack` packets to a `payload` packet. Sequences without `payload` packet should also be valid. Example packet sequence:

```
| 0x1 | 0x0 | hash | 0x1 | 0x1 | hash | 0x0 | 0x2 | payload |
```

This packet sequence will acknowledge received packets with ids `0` and `1` and will send packet with id `2`.

Upon reading `ack` packets receiver should validate received hash for each packet.

## App Programming Interface

*App* programming interface (located within the `skywire/pkg/app` module) should expose methods for *Apps* to connect to a piped connection, perform handshake and exchange data with remote nodes.

*App* interface should expose following methods:

```golang
// Addr implements net.Addr for App connections.
type Addr struct {
	PubKey transport.PubKey
	Port   uint16
}

// LoopAddr stores addressing parameters of a loop package.
type LoopAddr struct {
	Port   uint16
	Remote Addr
}

// Packet represents message exchanged between App and Node.
type Packet struct {
	Addr    *LoopAddr
	Payload []byte
}

// Config defines configuration parameters for an App
type Config struct {
	AppName         string
	AppVersion      string
	ProtocolVersion string
}

// Setup sets up an app using default pair of pipes and performs handshake.
func Setup(config *Config) (*App, error) {}

// Accept awaits for incoming loop confirmation request from a Node and
// returns net.Conn for a received loop.
func (app *App) Accept() (net.Conn, error) {}

// Dial sends create loop request to a Node and returns net.Conn for created loop.
func (app *App) Dial(raddr *Addr) (net.Conn, error) {}

// Addr returns empty Addr, implements net.Listener.
func (app *App) Addr() net.Addr {}

// Close implements io.Closer for an App.
func (app *App) Close() error {}
```

## App to Node Communication protocol

Communication between *Visor* and an *App* happens over the piped connection using binary multiplexed protocol.

The following is the expected format of a App Packet:

```
| Packet Len | Type   | Message ID | JSON Body |
| 2 bytes    | 1 byte | 1 byte     | ~         |
```

- ***Packet Len*** specifies the total packet length in bytes (exclusive of the *Packet Len* field).
- ***Type*** specifies the *App Packet Type*.
- ***Message ID*** specifies multiplexing ID of a message, response for this message should contain the same ID.
- ***JSON Body*** is the packet body (in JSON format) that is unique depending on the packet type.

**App Packet Types Summary:**

| Type | Name |
| ---- | ---- |
| 0x00 | `Init` |
| 0x01 | `CreateLoop` |
| 0x02 | `ConfirmLoop` |
| 0x03 | `Send` |
| 0x04 | `Close` |
| 0xfe | `ResponseFailure` |
| 0xff | `ResponseSuccess` |

### `0x00 Init`

Sent by an *App* to a *Visor*. This packet is used to handshake connection between an *App* and a *Visor*. *Visor* will typically check if app is allowed by the config file and which port should be statically allocated it.

**JSON Body:**

```json
{
    "app-name": "foo",
    "app-version": "0.0.1",
    "protocol-version": "0.0.1"
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` without body.

### `0x01 CreateLoop`

Sent by an *App* to a *Visor*. This packet is used to open new *Loop* to a remote *Visor*.

**JSON Body:**

```json
{
    "pk": "<remote-pk>",
    "port": <remote-port>
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with
    ```json
    {
        "pk": "<local-pk>",
        "port": <local-port>
    }
    ```

### `0x02 ConfirmLoop`

Sent by a *Visor* to an *App* to notify about request to open new *Loop* from a remote *Visor*

**JSON Body:**

```json
[
    {
        "pk": "<local-pk>",
        "port": <local-port>
    },
    {
        "pk": "<remote-pk>",
        "port": <remote-port>
    }
]
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty body.

### `0x03 Send`

Sent by a *Visor* and an *App*. This message is used to exchange messages through a previously established *Loop*.

**JSON Body:**

```json
{
    "addr": {
        "port": <local-port>,
        "remote": {
            "pk": "<remote-pk>",
            "port": <remote-port>
        }
    },
    "payload": "<binary-data>"
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty body.

### `0x04 Close`

Sent by a *Visor* and an *App*. *App* uses this message to notify about closed *Loop*. *Visor* sends this message after remote node is requested to close established *Loop*.

**JSON Body:**

```json
{
    "port": <local-port>,
    "remote": {
        "pk": "<remote-pk>",
        "port": <remote-port>
    }
}
```

Response:
- `ResponseFailure` with `error`.
- `ResponseSuccess` with empty body.

## App Node Configuration

The following is the JSON representation of a Skywire configuration.

```json
{
  "version": "1.0",
  "node": {
    "static_public_key": "024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7",
    "static_secret_key": "42bca4df2f3189b28872d40e6c61aacd5e85b8e91f8fea65780af27c142419e5"
  },
  "dmsg": {
    "discovery_addresses": ["http://localhost:9090"],
    "server_count": 1
  },
  "apps": [
    {
      "app": "helloworld",
      "version": "1.0",
      "auto_start": true,
      "port": 10,
      "args": []
    }
  ],
  "transport_discovery": "http://localhost:9091",
  "setup_nodes": ["02603d53d49b6575a0b8cee05b70dd23c86e42cd6cba99af769d61a6196ea2bcb1"],
  "trusted_nodes": ["0348c941c5015a05c455ff238af2e57fb8f914c399aab604e9abb5b32b91a4c1fe"],
  "dmsg_path": "./dmsg",
  "apps_path": "./apps",
  "local_path": "./local",
  "log_level": "info",
  "interfaces": {
    "rpc": ":3436"
  }
}
```

- `"version"` represents the version of the Skywire Node (and also the configuration format version).

- `"node"` includes the public/private keys that identify the node.

- `"dmsg"` configures the dmsg client instance included within the Skywire Node.
    - When `"public"` is set, the Dmsg Client Instance will advertise itself to discovery.
    - `"discovery_addresses"` specifies the Dmsg Discovery Services that the Skywire Node is to try.
    - `"server_count"` specifies the number of servers to ensure connection with on first startup.

- `"apps"` lists all available Skywire Apps. These configurations include; the App's name, whether the specified App should auto-start, and the ports that is reserved for the App. If these are not defined for an App, the App will not auto-start, nor have ports reserved for the App.
    - If `"version"` is not specified, the highest stable version will be selected.

- `"node_path"` stores logs, routing tables, and any data that the node may use.

- `"dmsg_path"` holds the path which the Dmsg Client Instance can use to store cache or additional configurations.

- `"apps_path"` holds all the app executables. App executable files should be named with no spaces or weird characters (TODO: define properly). They should also be appended with the semantic version of the App after a dot; `{app_name}.v{semantic_version}`.

- `"local_path"` contains the working directories of the apps. An app named `chat` of version `v1.0` should have a working directory within `{root_dir}/{local_path}/chat/v1.0/`. The contents of the App's working directory is specified by the App.

## App Node RPC Interface

The App node should attempt to connect to the assigned *Manager Node* on startup. The connection is to be encrypted via Noise (KK handshake pattern) so that the nodes can identify one another.

For the *App Node* to connect to the *Manager Node*, it needs the public key and tcp address of the *Manager Node* in it's configuration.

After connection has been established, the *App Node* becomes the RPC Server and the *Manager Node* becomes the RPC client that can execute commands on the *App Node* (the RPC Server).

Additionally, the App Node should listen on a port so that a local command-line executable (`skywire-cli`) can interact with it. This local port should hence, only accept connections from localhost.

### Commands

The following sub-commands should be supported. Note that command-line actions are listed below, but they should be served via RESTFUL interfaces.

**General:**

- **`skywire cli visor info`** obtains a summary of the current state of the ~~App Node~~ running skywire visor.

**App Management:**

- **`skywire cli visor app ls`** lists applications and applications stats (running/not running) (auto-start/non-auto-start) (local/remote ports). There should be flags for filtering (to be defined later).

- **`skywire cli visor app start <app>`** starts a Skywire app.

- **`skywire cli visor app stop <app>`** stops a Skywire app if running.

- **`add-autostart-app <app> [--start-now]`** adds a Skywire app to auto-start. After calling this command, the actual app does not actually start unless `--start-now` is set  - **NOT IMPLEMENTED** - set autostart behavior with `skywire cli config gen` or `skywire cli config update` or directly in the config

- **`rm-autostart-app <app> [--stop-now]`** removes an app from auto-starting. After calling this command, the actual app does not stop running unless `--stop-now` is set  - **NOT IMPLEMENTED** - set autostart behavior with `skywire cli config gen` or `skywire cli config update` or directly in the config

**Dmsg Management:**

- **`messaging list-discoveries`** lists saved discoveries and their statuses - **NOT IMPLEMENTED**

- **`messaging add-discovery <discovery-address>`** connects to and saves a discovery - **NOT IMPLEMENTED**

- **`messaging rm-discovery <discovery-address>`** disconnects from and removes a discovery  - **NOT IMPLEMENTED**

- **`skywire cli mdisc servers`** lists dmsg servers (just [available_servers](https://dmsgd.skywire.skycoin.com/dmsg-discovery/available_servers) not [all_servers](https://dmsgd.skywire.skycoin.com/dmsg-discovery/all_servers)) from dmsg discovery ~~and their statuses (connected/disconnected) (auto-connect/non-auto-connect)~~.

- **`messaging connect-server (<public-key>|--auto)`** connects to a dmsg server for this session (does not save server for auto-connect). If `--auto` is set, the transport discovery is queried for a random available dmsg server - **NOT IMPLEMENTED**

- **`messaging disconnect-server <public-key>`** disconnects from a dmsg server for this session (does not affect auto-connect settings) - **NOT IMPLEMENTED**

- **`messaging add-autoconnect-server <public-key> [--connect-now]`** adds a dmsg server to auto-connect. This command does not connect to the specified dmsg server unless `--connect-now` is set - **NOT IMPLEMENTED**

- **`messaging rm-autoconnect-server <public-key> [--disconnect-now]`** removes a dmsg server from auto-connecting. This command does not disconnect from the specified dmsg server unless `--disconnect-now` is set - **NOT IMPLEMENTED**

**Transport Management:**

- **`skywire cli tp --types <type>`** lists all transports by type used by the visor (represented as strings).
- **`skywire cli tp`** lists all transports associated with the visor.
- **`skywire cli tp add -t <transport-type> <remote-pk>`** adds a transport from the local visor to the remote public key of a given type.
- **`skywire cli tp rm [--tid=<transport-id>|--remote-pk=<remote-pk>|--all]`** removes a transport; either for a given transport ID, or all transports connected to a remote node (identified via the remote node's public key).

**Routes Management:**

- **`skywire cli route`** lists all routing rules. A route ID range filter can be specified.
- **`skywire cli route add`** add routing rules
- **`skywire cli route rm`** removes routing rules; either via a list of route ID keys, or via a range of route ID keys (note that routing rules are identified via their `<rid>` key). This action may consequently destroy loops, and may cause the *Route Setup Node* to request destruction of more routing rules.
- **`skywire cli route find`** Query the route finder to find routes between two keys.

**Loops Management:**

- **`list-loops [--local-port=<port>] [--remote-addr=<remote-pk>[:<remote-port>]]`** lists all loops. A local port filter can be specified, where the returned loops will only be of the specified local port (there is an equivalent remote address filter) - **NOT IMPLEMENTED**
- **`add-loop --local-port=<port> --remote-addr=<remote-pk>:<remote-port> [--setup-node=<pk>]`** attempts to create a loop with the assigned setup node. The setup node is automatically chosen if not specified - **NOT IMPLEMENTED**

## Ports Management

Within the `AppsConfig` file, ports are reserved for certain Apps. The following rules are to be opposed:
- Ports are either "reserved" or "unreserved".
    - No two Apps are allowed to "reserve" the same port.
    - Ports are reserved via the `AppsConfig` file.
- Reserved ports are either "active" or "inactive".
    - A port is "active" when the port is "reserved" for an App, and that App is running.
    - A port is "inactive" either when the port is "unclaimed", or when the port is "claimed" but the App is not running.

## App Example

Simple `ping-pong` client and server apps can be implemented in such way:

Server:

```golang
package server

import (
	"log"

	"github.com/watercompany/skywire/pkg/app"
)

func main() {
    // Open connection with Node
	helloworldApp, err := app.Setup(&app.Config{AppName: "helloworld-server", AppVersion: "1.0", ProtocolVersion: "0.0.1"})
	if err != nil {
		log.Fatal("Setup failure: ", err)
	}
	defer helloworldApp.Close()

	log.Println("listening for incoming connections")
    // Start listening loop
	for {
        // Wait for new Loop
		conn, err := helloworldApp.Accept()
		if err != nil {
			log.Fatal("Failed to accept conn: ", err)
		}

		log.Println("got new connection from:", conn.RemoteAddr())
        // Handle incoming connection
		go func() {
			buf := make([]byte, 4)
			if _, err := conn.Read(buf); err != nil {
				log.Println("Failed to read remote data: ", err)
			}

			log.Printf("Message from %s: %s", conn.RemoteAddr().String(), string(buf))
			if _, err := conn.Write([]byte("pong")); err != nil {
				log.Println("Failed to write to a remote node: ", err)
			}
		}()
	}
}
```

Client:

```golang
package server

import (
	"log"
	"os"

	"github.com/watercompany/skywire/pkg/app"
	"github.com/watercompany/skywire/pkg/cipher"
)

func main() {
    // Open connection with Node
	helloworldApp, err := app.Setup(&app.Config{AppName: "helloworld-client", AppVersion: "1.0", ProtocolVersion: "0.0.1"})
	if err != nil {
		log.Fatal("Setup failure: ", err)
	}
	defer helloworldApp.Close()

    // Read remote PK from stdin
	remotePK := cipher.PubKey{}
	if err := remotePK.UnmarshalText([]byte(os.Args[1])); err != nil {
		log.Fatal("Failed to construct PubKey: ", err, os.Args[1])
	}

    // Dial to remote Node
	conn, err := helloworldApp.Dial(&app.Addr{PubKey: remotePK, Port: 10})
	if err != nil {
		log.Fatal("Failed to open remote conn: ", err)
	}

    // Send payload
	if _, err := conn.Write([]byte("ping")); err != nil {
		log.Fatal("Failed to write to a remote node: ", err)
	}

    // Receive payload
	buf := make([]byte, 4)
	if _, err = conn.Read(buf); err != nil {
		log.Fatal("Failed to read remote data: ", err)
	}

	log.Printf("Message from %s: %s", conn.RemoteAddr().String(), string(buf))
}
```

# Manager Node

__Note: this section is using old terminology to refer to a visor running with hypervisor UI and remote visors which are configured to connect to that hypervisor__

The *Manager Node* is responsible for managing *App Nodes* and is identified via it's public key and TCP address.

The *App Node* is responsible for including a trusted *Manager Node* in it's configuration file, and attempt to connect to it on startup. The connection is authenticated and encrypted via the Noise protocol where the `XK` handshake pattern is used with the *App Node* being the initiator.

After the connection is successfully established between an *App Node* and a *Manager Node*, the *App Node* acts as the RPC server while the *Manager Node* is the RPC client. In this way, the *Manager Node* can execute commands on the *App Node*.

The *Manager Node* serves a REST API which the end user can interact with. To access the API, the user is required to log in via a username and password.

The *Manager Node* should be implemented in `/pkg/manager-node`.

## Manager Node REST API (and User Interface)

- Login/logout.
- Change password.
- List connected *App Nodes*, each App Node should contain the following summary; Public key, local address, number of transports established, number of apps running, Uptime.
- The user can click into a listed *App Node* and perform node-specific actions; specifically, the [RPC commands as specified above.](#app-node-rpc-interface)

# Glossary

**Skywire Transport:**

Identified via a Transport ID, a Skywire Transport is represented as an interface and can be implemented as different transport types. It is a bi-directional line of communication between two Skywire nodes (transport edges) and constructs a single hop of a route.

It is the responsibility of a Skywire Factory to generate Skywire Transports.

**Skywire Transport ID:**

A Skywire Transport is an uint16 integer that refers to a Skywire Transport and identifies it in the transport discovery. Skywire Transport IDs are assigned uniquely by and for a transport edge. Therefore, the same transport can be referenced by two different Transport IDs, assigned by the 2 edges.

**Transport Edge:**

A transport edge is one of the two Skywire nodes that make up a transport. It is represented by a unique public key that identifies the Skywire node.

**Transport Type:**

A Transport Type (represented by a string) refers to the underlying implementation of a Transport Factory or a Transport. A Transport Factory of a certain type can only construct Transports of that type.

Initially, we will have two transport types; dmsg and native. The dmsg transport is implemented by dmsg. The native transport is non-dependent on the current internet and in the future will be the main transport implementation for Skywire.

**Transport Factory:**

A transport factory is used by a Skywire node which constructs transports of a certain transport type. It is an interface that can either dial or listen for remotely initiated transports.

**Transport Perspective:**

A transport perspective is the assumed state of a transport (number of packets and bandwidth sent and received over that transport; whether transport is up or down). It only represents the perspective (on a transport) from a single transport edge and therefore the perspectives of two edges on the same transport might conflict.

**Transport Discovery:**

The transport discovery is a service that registers transports and the associated transport perspectives. It thereby provides the basis for the route finding service because it represents the public network topology. It is queried by the route finding service to discover routes.

**Hop:**

A hop is equivalent to a transport and is a single unit of a route.

**Skywire Route:**

A Skywire Route is a unidirectional network path that allows apps and services to communicate. It can be made up of one or more hops between Skywire Nodes. It is identified by route IDs, interpreted by individual Skywire Nodes via the routing table.

**Skywire Route ID:**

A route ID is represented by 32 bits and is the basis for a Skywire Node’s routing rules. Route IDs are changed along every hop of a route and there is a unique set of route IDs for every node.

**Route Finder:**

The route finder is a service that evaluates the network topology via the information of the transport discovery to provide possible routes to inquiring Skywire nodes. Currently it evaluates possible routes only on the basis of the hop metric.


**Routing Table:**

The Routing Table is a key-value store that determines the action to be performed on an incoming Packet.

Using the Routing ID (from the Packet) as the key, a Skywire Node can obtain either the next transport that the packet is to be sent over (and the new Routing ID) or whether the packet is to be consumed by the node itself.

**Stream:**

A stream represents a bi-directional line of communication between two Skywire Apps. A stream can use multiple Routes to establish itself.

**Skywire App:**

A Skywire App is an executable that interacts with the Skywire App Node via Unix pipes. Skywire apps provide services for the end user such as a proxy, ssh, chat, etc.

**Dmsg:**

dmsg is primitive fallback implementation of Transport and Transport Factory over the Internet. It consists of a dmsg discovery, client and server instances, where the server instance relays messages between clients. The communication between two clients is called a dmsg channel.

**Dmsg Instance:**

Dmsg instances (alongside the dmsg discovery) are the main components of dmsg. A dmsg instance can be either a dmsg server or dmsg client.

**Dmsg Link:**

A dmsg link is the direct line of communication between a server instance and a client instance. A dmsg channel between two client instances can be constructed from two dmsg links to a shared server instance.

**Dmsg Discovery:**

The dmsg discovery is a key value store that registers the messenger servers that a given messenger client is connected to. It allows clients to get the information necessary to establish a dmsg transport with another node by querying the public keys associated dmsg servers.

**Dmsg Channel:**    

A Dmsg Channel represents the bi-directional connection-oriented line of communication between two client instances of dmsg.

**Skywire Node:**

A Skywire Node (now skywire __visor__) is the general term for nodes that make up the Skywire Network. Any entity that manages Transports and Packets is considered to be a Skywire Node.

Examples of Skywire Nodes include; App Node (which manage Skywire Apps), Control Node (which administers App Nodes) and the Route Setup Node (which coordinates the construction of Routes with App Nodes).

**Skywire App Node:**

An App Node is a Skywire Node that runs, stops, monitors and sets permissions for Skywire Apps. Internally, it handles and coordinates packets incoming and outgoing between the Skywire App and the external Skywire network.

An App Node can also forward packets to external Skywire Nodes (based on the set Routing Rules).

**Skywire Control Nodes:**

A Skywire Control node is similar to an app node, with the difference that it has administrative permissions for other nodes in a cluster and is being sent logs from other nodes. It should not run user-based Skywire Apps, nor should it forward Packets.

**Skywire Route Setup Node:**

The Route Setup Node is a Skywire Node which runs a service that allows it to set up routes for other Skywire nodes. It does this by relaying the routing rules to individual Skywire App Nodes.

**Skywire Transsport Setup Node:**

The Transport Setup Node is a service which is permissioned via inclusion of it's public key in the visor's config by other visors on the network that allows it to set up transports between remote visors or Skywire nodes. It does this via a special api which is exposed over dmsg to any keys which are whitelisted in the transport setup nodes array in the visor's config.
