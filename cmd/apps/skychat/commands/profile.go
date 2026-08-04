// Package commands cmd/apps/skychat/commands/profile.go c4-app-chat
//
// The browser's door to skychat profiles: read and write this visor's own
// published name and avatar, and look up a peer's.
//
// Same shape as group.go's handlers and for the same reasons — the visor
// owns the store and the wire, this file owns nothing but the HTTP verb
// mapping and the error codes a UI can act on. Gated on --pair-enable
// because everything here goes through the visor RPC seam; without it the
// routes are not registered at all, which the UI reads as 404 and treats
// as "this build has no profiles" rather than "the request failed".
package commands

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/address"
	"github.com/skycoin/skywire/pkg/visor"
)

// registerProfileHTTPHandlers wires the /profile endpoints onto mux.
//
// "/profile/peer" is registered before "/profile" would ever see it; both
// are exact patterns, so net/http's longest-match rule keeps them apart
// with no prefix handler in between.
func registerProfileHTTPHandlers(mux *http.ServeMux) {
	if !pairEnable {
		return
	}
	mux.HandleFunc("/profile", requireAuthFunc(profileHandler()))
	mux.HandleFunc("/profile/peer", requireAuthFunc(profilePeerHandler()))
}

// profileHandler serves this visor's own profile:
//
//	GET  /profile   → what we publish (plus our address, always)
//	POST /profile   → set it; {"clear":true} removes it
//
// A GET never fails for "nothing set": an empty profile carrying the key
// and the address is the correct answer, and it is exactly what the "my
// address" dialog needs from a visor whose operator has typed nothing.
func profileHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !pairRPCAlive() {
			http.Error(w, "profile unavailable (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		switch r.Method {
		case http.MethodGet:
			var p visor.Profile
			if err := pairRPCCall("ProfileGet", func(c visor.API) error {
				out, e := c.ProfileGet()
				p = out
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, p)

		case http.MethodPost:
			var body visor.ProfileSetArgs
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
				return
			}
			var p visor.Profile
			if err := pairRPCCall("ProfileSet", func(c visor.API) error {
				out, e := c.ProfileSet(body)
				p = out
				return e
			}); err != nil {
				// A rejected avatar is the user's input being wrong, not the
				// visor being broken — 400 so the UI shows the reason next to
				// the file picker instead of a generic failure toast.
				code := http.StatusInternalServerError
				if isProfileInputError(err) {
					code = http.StatusBadRequest
				}
				http.Error(w, err.Error(), code)
				return
			}
			writeJSON(w, p)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// profilePeerHandler serves GET /profile/peer?pk=… — "who is this key?".
//
// Answers 404 when the peer cannot be reached or says nothing, because to
// the caller those are the same actionable fact: there is no name to show,
// fall back to the key. The UI treats this as routine and silent — a
// contact with no published profile is the common case, not an error worth
// a red bar.
func profilePeerHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !pairRPCAlive() {
			http.Error(w, "profile unavailable (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		raw := strings.TrimSpace(r.URL.Query().Get("pk"))
		if raw == "" {
			http.Error(w, "pk required", http.StatusBadRequest)
			return
		}
		// Accept a full skychat:// address as well as a bare key: the UI
		// hands over whatever the user pasted, same as /group/catalog.
		var pk cipher.PubKey
		if addr, err := address.Parse(raw); err == nil {
			pk = addr.PK
		} else if err := pk.Set(raw); err != nil {
			http.Error(w, "invalid pk: "+err.Error(), http.StatusBadRequest)
			return
		}
		var p visor.Profile
		if err := pairRPCCall("ProfileFetch", func(c visor.API) error {
			out, e := c.ProfileFetch(pk)
			p = out
			return e
		}); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, p)
	}
}

// isProfileInputError reports whether an error from ProfileSet is about
// what the user supplied rather than about the visor.
//
// Matched on the error text because the error crosses a net/rpc boundary,
// which flattens every wrapped error into a string — errors.Is cannot work
// here and pretending otherwise would silently classify everything as a
// server fault. The strings come from pkg/skychat/profile's sentinels.
func isProfileInputError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, s := range []string{"avatar too large", "avatar must be"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
