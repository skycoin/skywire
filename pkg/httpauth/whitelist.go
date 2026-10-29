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
	// ErrNotDmsg means the request did not arrive over dmsg or skynet, so it has
	// no authenticated caller.
	ErrNotDmsg = errors.New("whitelisted endpoints are served over dmsg only")
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
// status to answer with. The caller is the authenticated remote key of a dmsg
// stream, which includes a stream a host visor hands over from skynet. A plain
// HTTP request has no such key and is refused.
func (wl Whitelist) Authorize(r *http.Request) (cipher.PubKey, int, error) {
	pk, ok := dmsgRemotePK(r)
	if !ok {
		return cipher.PubKey{}, http.StatusForbidden, ErrNotDmsg
	}
	if !wl.Get(pk.Hex()) {
		return cipher.PubKey{}, http.StatusForbidden, ErrNotWhitelisted
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
