// Package api pkg/discovery/api/error_handler.go
package api

import (
	"errors"
	"net/http"

	"github.com/skycoin/dmsg/pkg/disc"
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

	if code != http.StatusNotFound {
		a.log(r).Warnf("%d: %s", code, e)
	}

	a.writeJSON(w, r, code, disc.HTTPMessage{Code: code, Message: msg})
}
