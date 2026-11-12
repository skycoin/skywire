// Package visor pkg/visor/hypervisor_handlers_ui2_mail.go c3-vis-core
package visor

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/ui2"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The mail tab of the server rendered UI. Each action answers with the whole
// tab, so the folder counts stay current. Attachments and the raw message
// download from the JSON routes.

// ui2MailMaxUpload caps a compose form with its attachments.
const ui2MailMaxUpload = 64 << 20

func (hv *Hypervisor) ui2MailRoutes(r *httputil.Router) {
	r.Get("/visors/{pk}/mail", hv.ui2MailView("list"))
	r.Get("/visors/{pk}/mail/list", hv.ui2MailView("list"))
	r.Get("/visors/{pk}/mail/message", hv.ui2MailView("message"))
	r.Get("/visors/{pk}/mail/compose", hv.ui2MailView("compose"))
	r.Get("/visors/{pk}/mail/settings", hv.ui2MailView("settings"))
	r.Delete("/visors/{pk}/mail/message", hv.ui2MailDelete())
	r.Post("/visors/{pk}/mail/retry", hv.ui2MailRetry())
	r.Post("/visors/{pk}/mail/send", hv.ui2MailSend())
	r.Post("/visors/{pk}/mail/enable", hv.ui2MailEnable())
	r.Post("/visors/{pk}/mail/whitelist", hv.ui2MailWhitelist())
	r.Post("/visors/{pk}/mail/helpers", hv.ui2MailHelpers())
}

// ui2MailLoad fills the status and, for the list view, the folder.
func ui2MailLoad(ctx *httpCtx, m *ui2.Mail) {
	st, err := ctx.API.MailStatus()
	if err != nil {
		m.StatusErr = err.Error()
	}
	m.Status = st
	if m.View != "list" {
		return
	}
	list, err := ctx.API.MailList(m.Folder)
	if err != nil {
		m.Err = err.Error()
	}
	m.List = list
}

// ui2MailOf reads the folder and message id from the query.
func ui2MailOf(r *http.Request, view string) ui2.Mail {
	q := r.URL.Query()
	return ui2.Mail{PK: r.PathValue("pk"), View: view, Folder: ui2.Folder(q.Get("folder")), MsgID: q.Get("id")}
}

// ui2MailRender serves the page, or the tab body for an htmx request.
func (hv *Hypervisor) ui2MailRender(w http.ResponseWriter, r *http.Request, m ui2.Mail, toast ...string) {
	var cs []templ.Component
	if r.Header.Get("HX-Request") != "" {
		cs = append(cs, ui2.MailBody(m))
	} else {
		cs = append(cs, ui2.MailPage(m))
	}
	if len(toast) > 0 && toast[0] != "" {
		cs = append(cs, ui2.Toast(toast[0], len(toast) > 1))
	}
	hv.render(w, r, cs...)
}

func (hv *Hypervisor) ui2MailView(view string) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		m := ui2MailOf(r, view)
		switch view {
		case "message":
			msg, err := ctx.API.MailRead(m.Folder, m.MsgID)
			if err != nil {
				m.View, m.Err = "list", err.Error()
				break
			}
			m.Msg = msg
			m.Queue = ui2MailQueue(ctx, m.Folder, m.MsgID)
		case "compose":
			if id := r.URL.Query().Get("reply"); id != "" {
				if msg, err := ctx.API.MailRead(m.Folder, id); err == nil {
					m.Draft = ui2.Reply(m.Folder, msg)
				}
			}
		}
		ui2MailLoad(ctx, &m)
		hv.ui2MailRender(w, r, m)
	})
}

// ui2MailQueue is the delivery state of an Outbox message, from its folder
// listing, since reading a message does not carry it.
func ui2MailQueue(ctx *httpCtx, folder, id string) *skymail.Queued {
	if folder != ui2.FolderOutbox {
		return nil
	}
	list, err := ctx.API.MailList(folder)
	if err != nil {
		return nil
	}
	for _, s := range list {
		if s.ID == id {
			return s.Queue
		}
	}
	return nil
}

func (hv *Hypervisor) ui2MailDelete() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		m := ui2MailOf(r, "list")
		if err := ctx.API.MailDelete(m.Folder, m.MsgID); err != nil {
			ui2MailLoad(ctx, &m)
			hv.ui2MailRender(w, r, m, err.Error(), "err")
			return
		}
		ui2MailLoad(ctx, &m)
		hv.ui2MailRender(w, r, m, hv.ui2T(r, "ui2.mail.deleted"))
	})
}

func (hv *Hypervisor) ui2MailRetry() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		m := ui2MailOf(r, "list")
		m.Folder = ui2.FolderOutbox
		if err := ctx.API.MailRetry(m.MsgID); err != nil {
			ui2MailLoad(ctx, &m)
			hv.ui2MailRender(w, r, m, err.Error(), "err")
			return
		}
		// The attempt runs in the background. The Outbox shows how it went.
		ui2MailLoad(ctx, &m)
		hv.ui2MailRender(w, r, m, hv.ui2T(r, "ui2.mail.retrying"))
	})
}

// ui2MailSend sends the compose form. With any recipient failed the form
// comes back with the report and the draft kept, as in the Angular tab.
func (hv *Hypervisor) ui2MailSend() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		// As postMailSend: delivery over dmsg can outlast the write timeout.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(mailSendWriteTimeout)) //nolint:errcheck
		r.Body = http.MaxBytesReader(w, r.Body, ui2MailMaxUpload)
		m := ui2.Mail{PK: r.PathValue("pk"), View: "compose", Folder: ui2.FolderInbox}
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			m.Err = err.Error()
			ui2MailLoad(ctx, &m)
			hv.ui2MailRender(w, r, m)
			return
		}
		f := r.MultipartForm.Value
		get := func(k string) string {
			if v := f[k]; len(v) > 0 {
				return v[0]
			}
			return ""
		}
		m.Draft = ui2.Draft{To: get("to"), Cc: get("cc"), Subject: get("subject"), Body: get("body"), InReplyTo: get("in_reply_to")}
		out := skymail.Outgoing{To: ui2.Addrs(m.Draft.To), Cc: ui2.Addrs(m.Draft.Cc), Subject: m.Draft.Subject, Body: m.Draft.Body, InReplyTo: m.Draft.InReplyTo}
		for _, fh := range r.MultipartForm.File["files"] {
			file, err := fh.Open()
			if err != nil {
				m.Err = err.Error()
				break
			}
			data, err := io.ReadAll(file)
			_ = file.Close() //nolint:errcheck,gosec
			if err != nil {
				m.Err = err.Error()
				break
			}
			ct := fh.Header.Get("Content-Type")
			if ct == "" {
				ct = "application/octet-stream"
			}
			out.Attachments = append(out.Attachments, skymail.OutgoingAttachment{Name: fh.Filename, ContentType: ct, Data: data})
		}
		if m.Err == "" && len(out.To) == 0 {
			m.Err = hv.ui2T(r, "ui2.mail.no-recipient")
		}
		if m.Err != "" {
			ui2MailLoad(ctx, &m)
			hv.ui2MailRender(w, r, m)
			return
		}
		res, err := ctx.API.MailSend(out)
		if res != nil {
			m.Report = res.Recipients
		}
		failed, queued := 0, 0
		for _, rc := range m.Report {
			switch {
			case rc.Queued:
				queued++
			case rc.Err != "":
				failed++
			}
		}
		if err != nil || failed > 0 {
			if err != nil {
				m.Err = err.Error()
			}
			ui2MailLoad(ctx, &m)
			hv.ui2MailRender(w, r, m)
			return
		}
		m.View, m.Folder, m.Draft = "list", ui2.FolderSent, ui2.Draft{}
		toast := hv.ui2T(r, "ui2.mail.sent")
		if queued > 0 {
			m.Folder, toast = ui2.FolderOutbox, hv.ui2T(r, "ui2.mail.queued")
		}
		ui2MailLoad(ctx, &m)
		hv.ui2MailRender(w, r, m, toast)
	})
}

func (hv *Hypervisor) ui2MailEnable() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		on := r.URL.Query().Get("on") == "1"
		m := ui2.Mail{PK: r.PathValue("pk"), View: "settings", Folder: ui2.FolderInbox}
		err := ctx.API.MailSetSettings(visorapi.MailSettingsUpdate{Enable: &on})
		ui2MailLoad(ctx, &m)
		if err != nil {
			hv.ui2MailRender(w, r, m, err.Error(), "err")
			return
		}
		toast := hv.ui2T(r, "ui2.mail.turned-off")
		if on {
			toast = hv.ui2T(r, "ui2.mail.turned-on")
		}
		hv.ui2MailRender(w, r, m, toast)
	})
}

func (hv *Hypervisor) ui2MailWhitelist() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		m := ui2.Mail{PK: r.PathValue("pk"), View: "settings", Folder: ui2.FolderInbox}
		if err := r.ParseForm(); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		pks, err := ui2.ParsePKList(r.PostForm.Get("pks"))
		if err == nil {
			err = ctx.API.MailSetWhitelist(pks)
		}
		ui2MailLoad(ctx, &m)
		if err != nil {
			hv.ui2MailRender(w, r, m, err.Error(), "err")
			return
		}
		hv.ui2MailRender(w, r, m, hv.ui2T(r, "ui2.mail.saved"))
	})
}

// ui2MailHelpers saves both lists. Empty means the default, "none" an empty
// list, as in the Angular tab.
func (hv *Hypervisor) ui2MailHelpers() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		m := ui2.Mail{PK: r.PathValue("pk"), View: "settings", Folder: ui2.FolderInbox}
		if err := r.ParseForm(); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		var u visorapi.MailSettingsUpdate
		var err error
		u.Helpers, u.HelpersDefault, err = ui2MailList(r.PostForm.Get("helpers"))
		if err == nil {
			u.RelayFor, u.RelayForDefault, err = ui2MailList(r.PostForm.Get("relay_for"))
		}
		if err == nil {
			err = ctx.API.MailSetSettings(u)
		}
		ui2MailLoad(ctx, &m)
		if err != nil {
			hv.ui2MailRender(w, r, m, err.Error(), "err")
			return
		}
		hv.ui2MailRender(w, r, m, hv.ui2T(r, "ui2.mail.saved"))
	})
}

func ui2MailList(text string) (*[]cipher.PubKey, bool, error) {
	t := strings.TrimSpace(text)
	switch {
	case t == "":
		return nil, true, nil
	case strings.EqualFold(t, "none"):
		return &[]cipher.PubKey{}, false, nil
	}
	pks, err := ui2.ParsePKList(t)
	return &pks, false, err
}
