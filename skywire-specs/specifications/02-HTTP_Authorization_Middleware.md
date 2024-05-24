# HTTP Authorization Middleware

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

- `SW-Public` - Specifies the public key (hexadecimal string representation) of the Skywire Node performing this operation.
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
