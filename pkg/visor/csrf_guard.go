// Package visor pkg/visor/csrf_guard.go c3-vis-core
package visor

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/skycoin/skywire/pkg/httputil"
)

// csrfGuard rejects a cross-origin write that carries no valid CSRF token.
// Same-origin pages and non-browser clients pass, as in http.CrossOriginProtection.
func (hv *Hypervisor) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hv.useCsrf() {
			next.ServeHTTP(w, r)
			return
		}
		if token := r.Header.Get(CSRFHeaderName); token != "" && verifyCSRFToken(token) == nil {
			next.ServeHTTP(w, r)
			return
		}
		if err := checkCrossOrigin(r); err != nil {
			httputil.WriteJSON(w, r, http.StatusForbidden, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var (
	errCrossOriginRequest               = errors.New("cross-origin request detected from Sec-Fetch-Site header")
	errCrossOriginRequestFromOldBrowser = errors.New("cross-origin request detected, and/or browser is out of date: Sec-Fetch-Site is missing, and Origin does not match Host")
)

// checkCrossOrigin is http.CrossOriginProtection.Check without trusted origins
// or bypass patterns. TinyGo's net/http does not have it.
func checkCrossOrigin(r *http.Request) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "":
	case "same-origin", "none":
		return nil
	default:
		return errCrossOriginRequest
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	if o, err := url.Parse(origin); err == nil && o.Host == r.Host {
		return nil
	}
	return errCrossOriginRequestFromOldBrowser
}
