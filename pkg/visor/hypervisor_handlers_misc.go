// Package visor pkg/visor/hypervisor.go
package visor

import (
	"net/http"
	"strings"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
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

// getPK serves GET /api/pk — returns the hypervisor's own pubkey
// as JSON. Unauthenticated by design: skybian's first-boot
// autoconfig (skymanager.sh) needs the pubkey to peer with the
// hypervisor before any account exists.
//
// Off by default — the route is only registered when
// HypervisorConfig.EnablePKEndpoint is true. The skybian /
// Arch-ARM image builds flip it on; vanilla hypervisor configs
// leave it off.
//
// Soft gate: the caller must send an SW-Public header naming its
// own (well-formed) cipher.PubKey. This is a "looks like another
// visor" check, not real auth — no nonce, no signature. It's
// enough to keep casual scanners from harvesting hypervisor
// pubkeys via random GETs without raising the bar so high that
// the autoconfig flow breaks.
//
// Response shape (snake_case to match other /api/ JSON envelopes):
//
//	{"public_key": "<66-hex>"}
func (hv *Hypervisor) getPK() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := strings.TrimSpace(r.Header.Get("SW-Public"))
		if caller == "" {
			httputil.WriteJSON(w, r, http.StatusUnauthorized, struct {
				Error string `json:"error"`
			}{Error: "SW-Public header required"})
			return
		}
		var callerPK cipher.PubKey
		if err := callerPK.Set(caller); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, struct {
				Error string `json:"error"`
			}{Error: "SW-Public header is not a valid public key"})
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, struct {
			PublicKey cipher.PubKey `json:"public_key"`
		}{
			PublicKey: hv.c.PK,
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
