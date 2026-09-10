// Package api pkg/dmsg/discovery/api/error_handler.go c1-net-dmsg
package api

import (
	"errors"
	"net/http"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

var apiErrors = map[error]func() (int, string){

	disc.ErrKeyNotFound: func() (int, string) {
		return http.StatusNotFound, disc.ErrKeyNotFound.Error()
	},

	disc.ErrUnexpected: func() (int, string) {
		return http.StatusInternalServerError, disc.ErrUnexpected.Error()
	},

	disc.ErrUnauthorized: func() (int, string) {
		return http.StatusUnauthorized, disc.ErrUnauthorized.Error()
	},

	disc.ErrBadInput: func() (int, string) {
		return http.StatusBadRequest, disc.ErrBadInput.Error()
	},
}

func (a *API) handleError(w http.ResponseWriter, r *http.Request, e error) {
	var (
		code int
		msg  string
	)

	if _, ok := e.(disc.EntryValidationError); ok {
		code = http.StatusUnprocessableEntity
		msg = e.Error()
	} else {
		f := func() (int, string) { return http.StatusInternalServerError, disc.ErrUnexpected.Error() }
		for target, handler := range apiErrors {
			if errors.Is(e, target) {
				f = handler
				break
			}
		}
		code, msg = f()
	}

	logAPIError(a.log(r), code, e)

	a.writeJSON(w, r, code, disc.HTTPMessage{Code: code, Message: msg})
}

// logAPIError records a rejected request at a level that matches whose fault
// it is.
//
// A 4xx is the remote peer's mistake and the response already tells it so;
// the discovery has nothing to act on. Logging those at warn let any peer
// fill this service's log at warn just by re-posting a stale entry — the 422
// "sequence field of new entry is not sequence of old entry" alone ran at ~88
// per two minutes in production, drowning the warnings that do mean something
// is wrong here. A 5xx is this service's own failure and stays at warn. A 404
// is the ordinary answer to a lookup for a key that was never registered, so
// it stays unlogged.
func logAPIError(log logrus.FieldLogger, code int, e error) {
	switch {
	case code == http.StatusNotFound:
		// A miss is a normal answer, not an error worth a line.
	case code >= http.StatusInternalServerError:
		log.Warnf("%d: %s", code, e)
	default:
		log.Debugf("%d: %s", code, e)
	}
}
