// Package visor pkg/visor/hypervisor_handlers_ui2_hvsettings.go c3-vis-core
package visor

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The hypervisor's own settings page. Theme, refresh rate and labels are
// browser settings, as in the Angular UI, so only the password reaches
// the server.

func (hv *Hypervisor) ui2HVSettingsRoutes(r *httputil.Router) {
	r.Get("/settings", func(w http.ResponseWriter, r *http.Request) {
		hv.render(w, r, ui2.HVSettingsPage(hv.c.EnableAuth && hv.users != nil))
	})
	r.Post("/settings/password", hv.ui2ChangePassword())
}

// ui2ChangePassword checks the form as the Angular password page does and
// changes the password through the /api/change-password handler.
func (hv *Hypervisor) ui2ChangePassword() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !hv.c.EnableAuth || hv.users == nil {
			http.NotFound(w, r)
			return
		}
		if hv.useCsrf() {
			if err := verifyCSRFToken(r.Header.Get(CSRFHeaderName)); err != nil {
				httputil.WriteJSON(w, r, http.StatusForbidden, err)
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		oldPw, newPw := r.PostForm.Get("old"), r.PostForm.Get("new")
		fail := func(key string) { hv.render(w, r, ui2.PasswordForm(hv.ui2T(r, key))) }
		switch {
		case oldPw == "":
			fail("settings.password.errors.old-password-required")
			return
		case len(newPw) < 6 || len(newPw) > 64:
			fail("settings.password.errors.new-password-error")
			return
		case newPw != r.PostForm.Get("repeat"):
			fail("settings.password.errors.passwords-not-match")
			return
		}
		body := struct {
			Old string `json:"old_password"`
			New string `json:"new_password"`
		}{oldPw, newPw}
		if cw := callJSON(w, r, hv.users.ChangePassword(), body); cw.code != http.StatusOK {
			if cw.code == http.StatusUnauthorized || cw.code == http.StatusForbidden {
				fail("settings.password.errors.bad-old-password")
				return
			}
			hv.render(w, r, ui2.PasswordForm(cw.errText()))
			return
		}
		// The change ends every session, so log in again.
		w.Header().Set("HX-Redirect", "login")
		w.WriteHeader(http.StatusNoContent)
	}
}
