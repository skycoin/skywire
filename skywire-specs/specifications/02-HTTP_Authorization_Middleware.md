# HTTP Authorization Middleware

| Status   | Implemented                                              |
|----------|----------------------------------------------------------|
| Revision | 2026-10-27, skywire c18b044dc |
| Code     | `pkg/httpauth`, `pkg/httpauthclient`                     |

## Abstract

The authorization middleware authenticates HTTP requests to deployment
services by a secp256k1 public key. A request carries a signature over its body
and a per-key security nonce that prevents replay. Requests that arrive over
dmsg are identified by the dmsg peer key instead.

## Overview

Deployment services such as the address resolver, transport discovery and
service discovery wrap their authenticated handlers in `httpauth.WithAuth`.
Visors call them through `httpauthclient.Client`. Both ends keep a counter of
the next expected nonce for the key.

## Addressing

The server exposes the nonce recovery endpoint at `/security/nonces/{pk}`
through `httpauth.NonceHandler`. The key is the last path segment.

## Data Formats

`httpauth.Nonce` and `httpauthclient.Nonce` are `uint64`, rendered as decimal.

Request headers, parsed by `httpauth.AuthFromHeaders`:

| Header | Value |
|---|---|
| `SW-Public` | Public key, hexadecimal. Required. |
| `SW-Nonce` | Nonce of this request, unsigned decimal. Required. |
| `SW-Sig` | Signature, hexadecimal. Required when verification is on. |

The client also sets `SW-PublicIP` when it was given a public address, and the
build version header (`buildinfo.VersionHeader`). The server does not use them
for authentication.

The signed value is `SHA256(body || nonce)` where `nonce` is its decimal ASCII
rendering appended to the verbatim body (`httpauth.PayloadWithNonce`,
`httpauth.Sign`, `cipher.SignPayload`). For body `{"a":1}` and nonce `7` the
payload is the string `{"a":1}7`.

Error responses are `httpauth.HTTPResponse`:

| JSON name | Type | Meaning |
|---|---|---|
| `error.message` | string | Reason. The status text when empty. |
| `error.code` | integer | HTTP status code. |

`httpauth.NextNonceResponse`:

| JSON name | Type | Meaning |
|---|---|---|
| `edge` | public key | The queried key. |
| `next_nonce` | uint64 | The nonce the next request MUST carry. |

## Interfaces

### HTTP API

`GET /security/nonces/{pk}` is not authenticated.

| Status | Condition |
|---|---|
| 200 | `NextNonceResponse`. The nonce is zero for an unknown key. |
| 400 | The last path segment is not a public key, or it is the zero key. |
| 500 | The nonce store failed. |

## Behaviour

### Server

The nonce store (`httpauth.NonceStore`) provides `Nonce`, `IncrementNonce` and
`Count`. `IncrementNonce` is atomic and returns the new value. Implementations
are an in-memory map and Redis (`INCR` on `nonces:<pk>`), selected by
`httpauth.NewNonceStore`.

For a request that does not arrive over dmsg, `WithAuth` runs these steps.

1. `AuthFromHeaders` rejects a missing or malformed header with 401.
2. The stored nonce is read. A store failure returns 500.
3. A `SW-Nonce` that differs from the stored nonce returns 401 with the
   message `SW-Nonce does not match`.
4. The body is read in full and the signature is verified. A bad signature
   returns 401. A body above the `httputil.LimitBody` cap returns 413.
5. The wrapped handler runs with the key in the request context
   (`httpauth.ContextAuthKey`, read with `httpauth.PKFromCtx`).
6. When the handler wrote a 2xx status, the server calls `IncrementNonce`.
   Any other status leaves the nonce unchanged.

The middleware has no key whitelist. Handlers that need one check
`PKFromCtx` themselves.

`MakeLoadTestingMiddleware` skips the nonce and signature checks and still
requires `SW-Public` and `SW-Nonce`.

### Dmsg callers

A request whose `RemoteAddr` parses as a public key arrived over dmsg HTTP.
The dmsg handshake authenticated that key. `WithAuth` takes the identity from
`RemoteAddr` (`dmsgRemotePK`), ignores any `SW-*` header, performs no nonce or
signature check, never increments a nonce, and calls the handler.

### Client

`httpauthclient.Client` keeps one nonce counter per server address and key,
shared by every client in the process that uses that pair, and serializes
their requests.

- `NewClient` fetches the initial nonce from `/security/nonces/{pk}`.
- Each request carries the three `SW-*` headers computed from the local
  counter.
- When the response is a JSON error with code 401 and message
  `SW-Nonce does not match`, the client fetches the nonce again, stores it and
  retries the request once.
- After the final response the client increments its counter when the status
  is 2xx.

### Deregistration whitelist

The bulk deregistration endpoints of transport discovery, dmsg discovery, the
address resolver and service discovery do not use the nonce middleware. They
authorize the caller with `httpauth.Whitelist.Authorize` against the keys in
the service's `whitelist_keys` setting.

- A dmsg caller is identified by its dmsg remote key. It is authorized when
  that key is on the whitelist. The `NM-PK` and `NM-Sign` headers are ignored.
- A plain HTTP caller sends its key in hex in `NM-PK` and, in `NM-Sign`, a
  signature by that key over the hex of the key.

A key that is not listed, or a signature that does not verify, answers 403
(`ErrNotWhitelisted`). A header that does not parse answers 400
(`ErrBadWhitelistAuth`). The key lists that three of these endpoints take in
the body are read with `httpauth.ReadKeyList`, a JSON array of keys in hex.

## Security Considerations

The nonce is per key and per service. It orders requests and prevents replay of
a signed body; it does not encrypt.
Over dmsg the stream is authenticated and ordered by the dmsg session, so
the application level nonce is not used.

The plain HTTP whitelist signature covers only the caller's key, so a
captured header pair authorizes its holder until the key leaves the whitelist.
Over dmsg the headers are not trusted.

## References

- [04 Transport Discovery](04-Transport_Discovery.md)
- [20 Address Resolver](20-Address_Resolver.md)
