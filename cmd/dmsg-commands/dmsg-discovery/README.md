# `dmsg.Discovery`

## Entry

An entry within the `dmsg.Discovery` can either represent a `dmsg.Server` or a `dmsg.Client`. The `dmsg.Discovery` is a key-value store, in which entries (of either server or client) use their public keys as their "key".

The following is the representation of an Entry in Golang.

```golang
// Entry represents an entity's entry in the Discovery database.
type Entry struct {
    // The data structure's version.
    Version string `json:"version"`

    // A Entry of a given public key may need to iterate. This is the iteration sequence.
    Sequence uint64 `json:"sequence"`

    // Timestamp of the current iteration.
    Timestamp int64 `json:"timestamp"`

    // Public key that represents the entity.
    Static cipher.PubKey `json:"static"`

    // Contains the entity's required client meta if it's to be advertised as a Client.
    Client *Client `json:"client,omitempty"`

    // Contains the entity's required server meta if it's to be advertised as a Server.
    Server *Server `json:"server,omitempty"`

    // Signature for proving authenticity of of the Entry.
    Signature cipher.Sig `json:"signature,omitempty"`
}

// Client contains the entity's required client meta, if it is to be advertised as a Client.
type Client struct {
    // DelegatedServers contains a list of delegated servers represented by their public keys.
    DelegatedServers []cipher.PubKey `json:"delegated_servers"`
}

// Server contains the entity's required server meta, if it is to be advertised as a dmsg Server.
type Server struct {
    // IPv4 or IPv6 public address of the dmsg Server.
    Address string `json:"address"`

    // Number of connections still available.
    AvailableConnections int `json:"available_connections"`
}
```

**Definition rules:**

- A record **MUST** have either a "Server" field, a "Client" field, or both "Server" and "Client" fields. In other words, a dmsg node can be a dmsg Server, a dmsg Client, or both a dmsg Server and a dmsg Client.

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

## Endpoints

Only 3 endpoints need to be defined; Get Entry, Post Entry, and Get Available Servers.

### GET Entry

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

### POST Entry

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

### GET Available Servers

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

### GET All Servers

Obtains a subset of all server entries.

> `GET {domain}/discovery/all_servers`

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
