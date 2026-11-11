// Package visor pkg/visor/hypervisor_handlers_ui2_apps.go c3-vis-core
package visor

import (
	"net/http"
	"time"

	"github.com/a-h/templ"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The apps tab of the server rendered UI. Writes go through the JSON putApp
// handler, so start, stop and settings behave exactly as in the Angular tab.

func (hv *Hypervisor) ui2AppsRoutes(r *httputil.Router) {
	r.Get("/ui2/visors/{pk}/apps", hv.ui2Apps(false))
	r.Get("/ui2/visors/{pk}/apps/rows", hv.ui2Apps(true))
	r.Get("/ui2/visors/{pk}/apps/{app}/settings", hv.ui2AppSettings())
	r.Put("/ui2/visors/{pk}/apps/{app}/settings", hv.ui2AppSettingsSave())
	r.Get("/ui2/visors/{pk}/apps/{app}/logs", hv.ui2AppLogs())
	r.Get("/ui2/visors/{pk}/apps/{app}/help", hv.ui2AppHelp())
	r.Put("/ui2/visors/{pk}/apps/{app}/state", hv.ui2AppPut("run", "status"))
	r.Put("/ui2/visors/{pk}/apps/{app}/autostart", hv.ui2AppPut("autostart", "autostart"))
}

// ui2CallAPI runs a JSON handler on body. On failure it passes the JSON error
// on, which ui2.js shows as a toast, and reports false.
func ui2CallAPI(w http.ResponseWriter, r *http.Request, h http.Handler, body any) bool {
	cw := callJSON(w, r, h, body)
	if cw.code == http.StatusOK {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(cw.code)
	_, _ = w.Write(cw.body.Bytes()) //nolint:errcheck,gosec
	return false
}

func ui2LoadApps(ctx *httpCtx, pk string) ui2.Apps {
	states, err := ctx.API.Apps()
	a := ui2.AppsOf(pk, states)
	if err != nil {
		a.Err = err.Error()
	}
	return a
}

// ui2Apps serves the tab, or with rows set only the polled lists.
func (hv *Hypervisor) ui2Apps(rows bool) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		a := ui2LoadApps(ctx, r.PathValue("pk"))
		if rows {
			hv.render(w, r, ui2.AppsRows(a))
			return
		}
		hv.render(w, r, ui2.AppsPage(a))
	})
}

// ui2AppPut sets one putApp field from a form value: "run" to status 1 or 0,
// "autostart" to true or false. The lists come back with a toast.
func (hv *Hypervisor) ui2AppPut(form, field string) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		on := r.FormValue(form) != ""
		var v any = on
		if field == "status" {
			v = statusStop
			if on {
				v = statusStart
			}
		}
		if !ui2CallAPI(w, r, hv.putApp(), map[string]any{field: v}) {
			return
		}
		a := ui2LoadApps(ctx, r.PathValue("pk"))
		hv.render(w, r, ui2.AppsRows(a), ui2.Toast(hv.ui2T(r, "apps.operation-completed"), false))
	})
}

func (hv *Hypervisor) ui2AppSettings() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		pk := r.PathValue("pk")
		x, ok := ui2LoadApps(ctx, pk).Find(r.PathValue("app"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		hv.render(w, r, ui2.AppSettings(pk, x, ""))
	})
}

// ui2AppSettingsSave saves the form as the Angular settings panel does: the
// args line, the whole environment, the launcher mode and autostart.
func (hv *Hypervisor) ui2AppSettingsSave() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		if err := r.ParseForm(); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		body := map[string]any{
			"args":          r.PostForm.Get("args"),
			"env_full":      ui2.EnvLines(r.PostForm.Get("env")),
			"launcher_mode": r.PostForm.Get("launcher_mode"),
			"autostart":     r.PostForm.Get("autostart") != "",
		}
		cw := callJSON(w, r, hv.putApp(), body)
		pk := r.PathValue("pk")
		a := ui2LoadApps(ctx, pk)
		if cw.code != http.StatusOK {
			x, _ := a.Find(r.PathValue("app"))
			x.Args, x.Env, x.LauncherMode = r.PostForm.Get("args"), r.PostForm.Get("env"), r.PostForm.Get("launcher_mode")
			hv.render(w, r, ui2.AppSettings(pk, x, cw.errText()))
			return
		}
		hv.render(w, r, templ.NopComponent, ui2.AppsRowsOOB(a), ui2.Toast(hv.ui2T(r, "app-settings.saved"), false))
	})
}

// ui2AppLogMax caps the lines the log panel shows.
const ui2AppLogMax = 500

func (hv *Hypervisor) ui2AppLogs() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		name := r.PathValue("app")
		lines, err := ctx.API.LogsSince(time.Now().Add(-24*time.Hour), name)
		msg := ""
		if err != nil {
			msg = err.Error()
			if st, e := ctx.API.App(name); e == nil && st.Status != appserver.AppStatusRunning {
				msg = hv.ui2T(r, "apps.apps-list.unavailable-logs-error")
			}
		}
		if len(lines) > ui2AppLogMax {
			lines = lines[len(lines)-ui2AppLogMax:]
		}
		hv.render(w, r, ui2.AppLogs(r.PathValue("pk"), name, lines, msg))
	})
}

func (hv *Hypervisor) ui2AppHelp() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		text, err := ctx.API.AppHelp(r.PathValue("app"))
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		hv.render(w, r, ui2.AppHelp(text, msg))
	})
}
