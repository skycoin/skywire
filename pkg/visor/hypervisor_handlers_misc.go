// Package visor pkg/visor/hypervisor.go
package visor

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func (hv *Hypervisor) getPong() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`"PONG!"`)); err != nil {
			hv.log(r).WithError(err).Warn("getPong: Failed to send PONG!")
		}
	}
}

func (hv *Hypervisor) getCsrf() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if useCsrf {
			token, err := newCSRFToken()
			if err != nil {
				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
				return
			}

			httputil.WriteJSON(w, r, http.StatusOK, Csrf{
				Token: token,
			})
		} else {
			httputil.WriteJSON(w, r, http.StatusOK, Csrf{Token: ""})
		}
	}
}

func (hv *Hypervisor) getAbout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dmsgConnected := false
		dmsgSessions := 0
		if hv.dmsgC != nil {
			sessions := hv.dmsgC.AllSessions()
			dmsgSessions = len(sessions)
			dmsgConnected = dmsgSessions > 0
		}
		httputil.WriteJSON(w, r, http.StatusOK, About{
			PubKey:        hv.c.PK,
			Build:         buildinfo.Get(),
			DmsgConnected: dmsgConnected,
			DmsgSessions:  dmsgSessions,
		})
	}
}

func (hv *Hypervisor) getPty() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		customCommand := make(map[string][]string)
		customCommand["update"] = visorconfig.UpdateCommand()
		ctx.PtyUI.PtyUI.Handler(customCommand)(w, r)
	})
}
