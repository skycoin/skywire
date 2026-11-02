// Package api pkg/dmsg/discovery/api/inbox.go c1-net-dmsg
package api

import (
	"io"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgasync"
	"github.com/skycoin/skywire/pkg/httputil"
)

// getInbox returns a key's dmsg-async inbox record.
// URI: /dmsg-discovery/inbox/{pk}
// Method: GET
func (a *API) getInbox() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(r.PathValue("pk"))); err != nil {
			a.handleError(w, r, disc.ErrBadInput)
			return
		}
		raw, err := a.db.Inbox(r.Context(), pk)
		if err != nil {
			a.handleError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw) //nolint:errcheck
	}
}

// setInbox stores a key's dmsg-async inbox record. The record's own signature
// authorizes it, and it is kept until it expires. An older sequence number
// than the stored record's is refused.
// URI: /dmsg-discovery/inbox/{pk}
// Method: POST
func (a *API) setInbox() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(r.PathValue("pk"))); err != nil {
			a.handleError(w, r, disc.ErrBadInput)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			a.handleError(w, r, err)
			return
		}
		var rec dmsgasync.Record
		if err := json.Unmarshal(body, &rec); err != nil {
			a.handleError(w, r, disc.ErrBadInput)
			return
		}
		now := time.Now()
		if rec.PK != pk {
			a.handleError(w, r, disc.NewEntryValidationError("record is not for this key"))
			return
		}
		if err := rec.Verify(now); err != nil {
			a.handleError(w, r, disc.NewEntryValidationError(err.Error()))
			return
		}
		if oldRaw, err := a.db.Inbox(r.Context(), pk); err == nil {
			var old dmsgasync.Record
			if json.Unmarshal(oldRaw, &old) == nil && old.Seq > rec.Seq {
				a.handleError(w, r, disc.NewEntryValidationError("record is older than the stored one"))
				return
			}
		}
		// Store the canonical encoding, not the request body.
		canon, err := json.Marshal(rec)
		if err != nil {
			a.handleError(w, r, disc.ErrUnexpected)
			return
		}
		if err := a.db.SetInbox(r.Context(), pk, canon, rec.Expires.Sub(now)); err != nil {
			a.handleError(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, disc.MsgEntrySet)
	}
}
