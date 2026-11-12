// Package visor pkg/visor/hypervisor_handlers_ui2_alerts.go c3-vis-core
package visor

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/alerts"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The offline alerts part of the settings tab, as the Angular node alerts
// panel: mail addresses, delay, webhook, sender, SMTP relay, a test send,
// and the push devices.

func (hv *Hypervisor) ui2AlertsRoutes(r *httputil.Router) {
	r.Get("/ui2/visors/{pk}/settings/alerts", hv.ui2Alerts(""))
	r.Put("/ui2/visors/{pk}/settings/alerts", hv.ui2Alerts("save"))
	r.Post("/ui2/visors/{pk}/settings/alerts/test", hv.ui2Alerts("test"))
	r.Delete("/ui2/visors/{pk}/settings/alerts/push/{id}", hv.ui2Alerts("push"))
}

var (
	// ui2EmailRe and ui2WebhookRe are the Angular form's checks.
	ui2EmailRe   = regexp.MustCompile(`^[^\s@,]+@[^\s@,]+$`)
	ui2WebhookRe = regexp.MustCompile(`^https?://[^\s/]+`)
)

func ui2AlertsOf(pk string, st *alerts.Status) ui2.Alerts {
	a := ui2.Alerts{PK: pk, Delay: "5m0s", StartTLS: true}
	if st == nil {
		return a
	}
	s := st.Settings
	a.Emails, a.Webhook, a.From = strings.Join(s.EmailTo, ", "), s.Webhook, s.From
	if st.OfflineAfter != "" {
		a.Delay = st.OfflineAfter
	}
	if r := s.SMTP; r != nil {
		a.Relay, a.Host, a.Username, a.StartTLS, a.PasswordSet = true, r.Host, r.Username, r.StartTLS, r.PasswordSet
		if r.Port != 0 {
			a.Port = strconv.Itoa(r.Port)
		}
	}
	for _, w := range st.Watched {
		a.Watched++
		if !w.Online {
			a.Offline++
		}
	}
	for _, p := range s.Push {
		a.Push = append(a.Push, ui2.AlertPush{ID: p.ID, Label: p.Label, Endpoint: p.Endpoint})
	}
	return a
}

func ui2SplitList(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
}

// ui2AlertsForm checks the form as formError does and builds the settings,
// keeping the watch list, disk warning and push devices as read. An empty
// password keeps the relay's saved one (alerts.Merge).
func (hv *Hypervisor) ui2AlertsForm(r *http.Request, cur alerts.Settings) (alerts.Settings, string) {
	f := r.PostForm
	s := alerts.Settings{Webhook: strings.TrimSpace(f.Get("webhook")), From: strings.TrimSpace(f.Get("from")), Watch: cur.Watch, DiskWarnPercent: cur.DiskWarnPercent, Push: cur.Push}
	for _, e := range ui2SplitList(f.Get("emails")) {
		if !ui2EmailRe.MatchString(e) {
			return s, hv.ui2T(r, "ui2.alerts.bad-email", "v", e)
		}
		s.EmailTo = append(s.EmailTo, e)
	}
	if s.Webhook != "" && !ui2WebhookRe.MatchString(s.Webhook) {
		return s, hv.ui2T(r, "ui2.alerts.bad-webhook")
	}
	if s.From != "" && !ui2EmailRe.MatchString(s.From) {
		return s, hv.ui2T(r, "ui2.alerts.bad-from")
	}
	if err := s.OfflineAfter.UnmarshalJSON([]byte(strconv.Quote(f.Get("delay")))); err != nil {
		return s, err.Error()
	}
	if f.Get("relay") != "" {
		host := strings.TrimSpace(f.Get("host"))
		if host == "" {
			return s, hv.ui2T(r, "ui2.alerts.bad-host")
		}
		port := 0
		if p := strings.TrimSpace(f.Get("port")); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 || n > 65535 {
				return s, hv.ui2T(r, "ui2.alerts.bad-port")
			}
			port = n
		}
		s.SMTP = &alerts.SMTP{Host: host, Port: port, Username: strings.TrimSpace(f.Get("username")), Password: f.Get("password"), StartTLS: f.Get("starttls") != ""}
	}
	return s, ""
}

func (hv *Hypervisor) ui2Alerts(op string) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pk := r.PathValue("pk")
		st, err := ctx.API.AlertsStatus()
		if err != nil {
			hv.render(w, r, ui2.AlertsPanel(ui2.Alerts{PK: pk, Err: err.Error()}))
			return
		}
		var results []alerts.Result
		var toast string
		switch op {
		case "save":
			r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
			if err := r.ParseForm(); err != nil {
				httputil.WriteJSON(w, r, http.StatusBadRequest, err)
				return
			}
			s, bad := hv.ui2AlertsForm(r, st.Settings)
			if bad != "" {
				a := ui2AlertsOf(pk, st)
				f := r.PostForm
				a.Emails, a.Delay, a.Webhook, a.From = f.Get("emails"), f.Get("delay"), f.Get("webhook"), f.Get("from")
				a.Relay, a.Host, a.Port, a.Username, a.StartTLS = f.Get("relay") != "", f.Get("host"), f.Get("port"), f.Get("username"), f.Get("starttls") != ""
				a.Err = bad
				hv.render(w, r, ui2.AlertsPanel(a))
				return
			}
			if !ui2CallAPI(w, r, hv.putAlerts(), s) {
				return
			}
			toast = hv.ui2T(r, "ui2.alerts.saved")
		case "test":
			if results, err = ctx.API.AlertsTest(); err != nil {
				a := ui2AlertsOf(pk, st)
				a.Err = err.Error()
				hv.render(w, r, ui2.AlertsPanel(a))
				return
			}
		case "push":
			if !ui2CallAPI(w, r, hv.deleteAlertsPush(), nil) {
				return
			}
			toast = hv.ui2T(r, "apps.operation-completed")
		}
		if op != "" {
			if st, err = ctx.API.AlertsStatus(); err != nil {
				hv.render(w, r, ui2.AlertsPanel(ui2.Alerts{PK: pk, Err: err.Error()}))
				return
			}
		}
		a := ui2AlertsOf(pk, st)
		for _, res := range results {
			a.Results = append(a.Results, ui2.AlertResult{Channel: res.Channel, Target: res.Target, Err: res.Err})
		}
		if toast != "" {
			hv.render(w, r, ui2.AlertsPanel(a), ui2.Toast(toast, false))
			return
		}
		hv.render(w, r, ui2.AlertsPanel(a))
	})
}
