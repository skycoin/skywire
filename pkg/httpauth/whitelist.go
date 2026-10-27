// Package httpauth pkg/httpauth/whitelist.go c0-com-http
package httpauth

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
)

var (
	// ErrNotWhitelisted means the caller's key is not in the Whitelist.
	ErrNotWhitelisted = errors.New("caller is not whitelisted")
	// ErrBadWhitelistAuth means the NM-PK or NM-Sign header does not parse.
	ErrBadWhitelistAuth = errors.New("malformed whitelist authentication headers")
)

// Whitelist holds the keys, in hex, allowed to call the bulk deregistration
// endpoints of the deployment services.
type Whitelist map[string]struct{}

// NewWhitelist returns an empty Whitelist.
func NewWhitelist() Whitelist { return make(Whitelist) }

// Set adds the key pk, in hex.
func (wl Whitelist) Set(pk string) { wl[pk] = struct{}{} }

// Get reports whether the key pk, in hex, is listed.
func (wl Whitelist) Get(pk string) bool {
	_, ok := wl[pk]
	return ok
}

// Authorize returns the key of a caller allowed by the Whitelist, or the HTTP
// status to answer with. Over dmsg the caller is the noise-authenticated
// remote key and the headers are ignored. Over plain HTTP the caller sends its
// key in NM-PK and a signature of that key's hex in NM-Sign.
func (wl Whitelist) Authorize(r *http.Request) (cipher.PubKey, int, error) {
	if pk, ok := dmsgRemotePK(r); ok {
		if !wl.Get(pk.Hex()) {
			return cipher.PubKey{}, http.StatusForbidden, ErrNotWhitelisted
		}
		return pk, 0, nil
	}
	pkHex := r.Header.Get("NM-PK")
	if !wl.Get(pkHex) {
		return cipher.PubKey{}, http.StatusForbidden, ErrNotWhitelisted
	}
	var pk cipher.PubKey
	if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
		return cipher.PubKey{}, http.StatusBadRequest, fmt.Errorf("%w: NM-PK: %v", ErrBadWhitelistAuth, err)
	}
	var sig cipher.Sig
	if err := sig.UnmarshalText([]byte(r.Header.Get("NM-Sign"))); err != nil {
		return cipher.PubKey{}, http.StatusBadRequest, fmt.Errorf("%w: NM-Sign: %v", ErrBadWhitelistAuth, err)
	}
	if err := cipher.VerifyPubKeySignedPayload(pk, sig, []byte(pk.Hex())); err != nil {
		return cipher.PubKey{}, http.StatusForbidden, fmt.Errorf("%w: %v", ErrNotWhitelisted, err)
	}
	return pk, 0, nil
}

// ReadKeyList reads a request body holding a JSON array of public keys.
func ReadKeyList(r *http.Request) ([]cipher.PubKey, error) {
	var keys []string
	if err := httputil.ReadJSON(r, &keys); err != nil {
		return nil, err
	}
	pks := make([]cipher.PubKey, 0, len(keys))
	for _, k := range keys {
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(k)); err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		pks = append(pks, pk)
	}
	return pks, nil
}
