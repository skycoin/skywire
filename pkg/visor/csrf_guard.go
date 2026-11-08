// Package visor pkg/visor/csrf_guard.go c3-vis-core
package visor

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/httputil"
)

// csrfGuard rejects a cross-origin write that carries no valid CSRF token.
// Same-origin pages and non-browser clients pass, as in http.CrossOriginProtection.
func (hv *Hypervisor) csrfGuard(next http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hv.useCsrf() {
			next.ServeHTTP(w, r)
			return
		}
		if token := r.Header.Get(CSRFHeaderName); token != "" && verifyCSRFToken(token) == nil {
			next.ServeHTTP(w, r)
			return
		}
		if err := cop.Check(r); err != nil {
			httputil.WriteJSON(w, r, http.StatusForbidden, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}
