# Transport Discovery

The Transport Discovery is a service that exposes a RESTful interface and interacts with a database on the back-end.

The database stores *Transport Entries* that can be queried using their *Transport ID* or via a given *Transport Edge*. 

The process of submitting a *Transport Entry* is called *Registration* and a Transport cannot be deregistered. However, nodes that are an *Edge* of a *Transport*, can update their *Transport Status*, and specify whether the *Transport* is up or down.

Any state-altering RESTful call to the *Transport Discovery* is authenticated using signatures, and replay attacks are avoided by expecting an incrementing security nonce (all communication should be encrypted with HTTPS anyhow).

## Transport Discovery Procedures

This is a summary of the procedures that the *Transport Discovery* is to handle.

**Registering a Transport:**

Technically, *Transports* are created by the Skywire Nodes themselves via an internal *Transport Factory* implementation. The *Transport Discovery* is only responsible for registering *Transports* in the form of a *Transport Entry*.

When two Skywire Nodes establish a Transport connection between them, it is at first, unregistered in the *Transport Discovery*. The node that initiated the creation of the Transport (or the node that called the `(transport.Transport).Dial` method), is the node that is responsible for initiating the *Transport Settlement Handshake*.

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

**Obtaining Transports:**

There are two ways to obtain transports; either via the assigned *Transport ID*, or via one of the *Transport Edges*. There is no restriction as who can access this information and results can be sorted by a given meta.

## Security Procedures

**Incrementing Security Nonce:**

An *Incrementing Security Nonce* is represented by a `uint64` value.

To avoid replay attacks and unauthorized access, each public key of a *Skywire Node* is assigned an *Incrementing Security Nonce*, and is expected to sign it with the rest of the body, and include the signature result in the http header. The *Incrementing Security Nonce* should increment every time an" endpoint is called (except for the endpoint that obtains the next expected incrementing security nonce). An *Incrementing Security Nonce* is not operation-specific, and increments every time any endpoint is called by the given Skywire Node.

The *Transport Discovery* should store a table of expected next *Incrementing Security Nonce* for each public key of a *Skywire Node*. There is an endpoint `GET /security/nonces/{public-key}` that provides the next expected *Incrementing Security Nonce* for a given Node public key. This endpoint should be publicly accessible, but nevertheless, the *Skywire Nodes* themselves should keep a copy of their next expected *Incrementing Security Nonce*.

The only times an *Incrementing Security Nonce* should not increment is when:

- An invalid request is submitted (missing/extra fields, invalid signature).
- An internal server error occurs.

Initially, the expected *Incrementing Security Nonce* should be 0. When it is this value, the *Transport Discovery* should not have an entry for it.

Each operation should contain the following extra header entries:

- `SW-Public` - Specifies the public key of the Skywire Node performing this operation.
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
