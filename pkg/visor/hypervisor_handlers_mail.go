// Package visor pkg/visor/hypervisor_handlers_mail.go c3-vis-core
package visor

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The hypervisor's view of a visor's mailbox, for the dashboard's mail tab
// and the phone app's mail screen.
// Every route calls the visor's mail API, the same one `skywire cli mail` and
// the desk's mail window use, so it works for remote visors too.

// mailRoutes registers the mail routes.
func (hv *Hypervisor) mailRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/mail", hv.getMailStatus())
	r.Post("/visors/{pk}/mail/send", hv.postMailSend())
	r.Put("/visors/{pk}/mail/whitelist", hv.putMailWhitelist())
	r.Put("/visors/{pk}/mail/settings", hv.putMailSettings())
	r.Get("/visors/{pk}/mail/{folder}", hv.getMailList())
	r.Get("/visors/{pk}/mail/{folder}/{id}", hv.getMailMessage())
	r.Delete("/visors/{pk}/mail/{folder}/{id}", hv.deleteMailMessage())
	r.Get("/visors/{pk}/mail/{folder}/{id}/raw", hv.getMailRaw())
	r.Get("/visors/{pk}/mail/{folder}/{id}/attachments/{n}", hv.getMailAttachment())
}

func (hv *Hypervisor) getMailStatus() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		st, err := ctx.API.MailStatus()
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, st)
	})
}

func (hv *Hypervisor) getMailList() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		list, err := ctx.API.MailList(r.PathValue("folder"))
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		if list == nil {
			list = []skymail.Summary{}
		}
		httputil.WriteJSON(w, r, http.StatusOK, list)
	})
}

func (hv *Hypervisor) getMailMessage() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		m, err := ctx.API.MailRead(r.PathValue("folder"), r.PathValue("id"))
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusNotFound, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, m)
	})
}

func (hv *Hypervisor) getMailRaw() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		id := r.PathValue("id")
		raw, err := ctx.API.MailRaw(r.PathValue("folder"), id)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusNotFound, err)
			return
		}
		w.Header().Set("Content-Type", "message/rfc822")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".eml"))
		_, _ = w.Write(raw) //nolint:errcheck,gosec
	})
}

func (hv *Hypervisor) getMailAttachment() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil || n < 0 {
			httputil.WriteJSON(w, r, http.StatusBadRequest, fmt.Errorf("attachment number must be 0 or more"))
			return
		}
		a, err := ctx.API.MailAttachment(r.PathValue("folder"), r.PathValue("id"), n)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusNotFound, err)
			return
		}
		// Served as a download, never rendered in the dashboard's origin:
		// an attachment is whatever a peer sent.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", a.Name))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(a.Data) //nolint:errcheck,gosec
	})
}

func (hv *Hypervisor) deleteMailMessage() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		if err := ctx.API.MailDelete(r.PathValue("folder"), r.PathValue("id")); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, true)
	})
}

func (hv *Hypervisor) postMailSend() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var msg skymail.Outgoing
		if err := httputil.ReadJSON(r, &msg); err != nil {
			if err != io.EOF {
				hv.log(r).Warnf("postMailSend request: %v", err)
			}
			httputil.WriteJSON(w, r, http.StatusBadRequest, fmt.Errorf("malformed message: %w", err))
			return
		}
		if len(msg.To) == 0 {
			httputil.WriteJSON(w, r, http.StatusBadRequest, fmt.Errorf("a message needs at least one recipient"))
			return
		}
		res, err := ctx.API.MailSend(msg)
		if err != nil {
			// The per-recipient results say which address failed and why;
			// the error alone only says that nothing went out.
			body := map[string]any{"error": err.Error()}
			if res != nil {
				body["message_id"], body["recipients"] = res.MessageID, res.Recipients
			}
			httputil.WriteJSON(w, r, http.StatusBadGateway, body)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, res)
	})
}

func (hv *Hypervisor) putMailWhitelist() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var body struct {
			PKs []cipher.PubKey `json:"pks"`
		}
		if err := httputil.ReadJSON(r, &body); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, fmt.Errorf("malformed whitelist: %w", err))
			return
		}
		if err := ctx.API.MailSetWhitelist(body.PKs); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, true)
	})
}

func (hv *Hypervisor) putMailSettings() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var u visorapi.MailSettingsUpdate
		if err := httputil.ReadJSON(r, &u); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, fmt.Errorf("malformed settings: %w", err))
			return
		}
		if err := ctx.API.MailSetSettings(u); err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, true)
	})
}
