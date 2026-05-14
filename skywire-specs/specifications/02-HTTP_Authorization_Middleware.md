# HTTP Authorization Middleware

Skywire is composed of multiple services and nodes. Some of these
communicate via RESTful interfaces; some endpoints require
authentication and authorization.

Nodes are identified by public keys, so the authentication scheme
is based on public/private key cryptography. The curve used for
node identity is `secp256k1`; public keys are represented in RESTful
endpoints as lowercase hexadecimal strings.

## Authorization protocol

To prevent replay attacks and unauthorized access, each remote
entity (identified by its public key) is associated with a
monotonically increasing *Security Nonce*. The remote entity SHALL
sign the *Security Nonce* concatenated with the request body on
every authenticated request.

The next expected *Security Nonce* SHALL increment by one for each
request that the server processes successfully (HTTP 2xx response).
On any non-2xx response, the next expected nonce SHALL NOT change.

The initial next expected *Security Nonce* for a remote entity is
zero. Servers MAY omit storage entries for remotes whose
next-expected nonce is still zero.

## Request headers

An authenticated request SHALL carry the following headers (`SW`
denotes Skywire):

| Header      | Value |
|-------------|-------|
| `SW-Public` | The remote's public key, hexadecimal-encoded. |
| `SW-Nonce`  | The current Security Nonce for this request, decimal-encoded. |
| `SW-Sig`    | The signature, hexadecimal-encoded, over `SHA256(nonce_bytes \|\| body)`, where `nonce_bytes` is the 8-byte big-endian encoding of the nonce and `body` is the verbatim request body. |

The server SHALL reject a request with HTTP 401 if any of the
following hold:

- The public key in `SW-Public` is not whitelisted (when a
  whitelist is configured).
- The nonce in `SW-Nonce` does not match the server's next-expected
  nonce for the entity.
- The signature in `SW-Sig` does not verify against the
  reconstructed `SHA256(nonce_bytes || body)` digest using the
  declared public key.
- The request body length exceeds the server's configured maximum
  (when set).

## Nonce-recovery endpoint

The server SHALL expose an endpoint that returns the
next-expected Security Nonce for a given public key, so a remote
that has lost sync can recover without an out-of-band channel.
The endpoint response body SHALL be:

```json
{
    "edge": "<public-key-hex>",
    "next_nonce": <uint64>
}
```

The nonce-recovery endpoint itself is not authenticated.

## Storage contract

The server-side storage layer SHALL provide:

- An operation to retrieve the next-expected nonce for a given
  public key. The result SHALL be zero when no entry exists.
- An atomic operation to retrieve-and-increment the next-expected
  nonce for a public key in a single step, returning the
  post-increment value. The atomicity guarantee is required to
  prevent two concurrent requests from racing past the same nonce.
- An operation to enumerate the number of stored entries (for
  operational visibility).

## Client contract

A client implementation SHALL:

- Track the next-expected Security Nonce locally per server it
  talks to.
- On every authenticated request, set the three `SW-*` headers as
  described above using the locally tracked nonce.
- On a successful (2xx) response, increment its local nonce by one.
- On HTTP 401 with a `next_nonce` mismatch, re-sync by calling the
  nonce-recovery endpoint and retrying once. A second 401 indicates
  a real authorization failure (key not whitelisted, signature
  invalid) and SHALL NOT be retried automatically.
