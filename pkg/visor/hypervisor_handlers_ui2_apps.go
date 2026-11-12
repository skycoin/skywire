// Package visor pkg/visor/hypervisor_handlers_ui2_apps.go c3-vis-core
package visor

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
	"github.com/skycoin/skywire/pkg/visor/ui2"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// The apps tab of the server rendered UI. Writes go through the JSON putApp
// handler, so start, stop and settings behave exactly as in the Angular tab.

func (hv *Hypervisor) ui2AppsRoutes(r *httputil.Router) {
	r.Get("/ui2/visors/{pk}/apps", hv.ui2Apps(false))
	r.Get("/ui2/visors/{pk}/apps/rows", hv.ui2Apps(true))
	r.Post("/ui2/visors/{pk}/apps/bulk", hv.ui2AppsBulk())
	r.Get("/ui2/visors/{pk}/apps/catalog", hv.ui2Catalog(false))
	r.Get("/ui2/visors/{pk}/apps/catalog/body", hv.ui2Catalog(true))
	r.Post("/ui2/visors/{pk}/apps/catalog/add", hv.ui2CatalogAdd())
	r.Get("/ui2/visors/{pk}/apps/{app}/settings", hv.ui2AppSettings())
	r.Put("/ui2/visors/{pk}/apps/{app}/settings", hv.ui2AppSettingsSave())
	r.Get("/ui2/visors/{pk}/apps/{app}/logs", hv.ui2AppLogs())
	r.Get("/ui2/visors/{pk}/apps/{app}/help", hv.ui2AppHelp())
	r.Get("/ui2/visors/{pk}/apps/{app}/flags", hv.ui2AppFlags())
	r.Put("/ui2/visors/{pk}/apps/{app}/flags", hv.ui2AppFlagsSave())
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

// ui2LoadAppsFor is ui2LoadApps with the list filter of the request.
func ui2LoadAppsFor(ctx *httpCtx, r *http.Request) ui2.Apps {
	return ui2LoadApps(ctx, r.PathValue("pk")).Filter(ui2.AppFilterOf(r.FormValue))
}

// ui2Apps serves the tab, or with rows set only the polled lists.
func (hv *Hypervisor) ui2Apps(rows bool) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		a := ui2LoadAppsFor(ctx, r)
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
		if r.FormValue("view") == "vpn" {
			hv.render(w, r, ui2.VPNBody(ui2.VPNOf(ui2LoadApps(ctx, r.PathValue("pk")))), ui2.Toast(hv.ui2T(r, "apps.operation-completed"), false))
			return
		}
		if r.FormValue("view") == "catalog" {
			hv.render(w, r, ui2.CatalogBody(ui2LoadCatalog(ctx, r.PathValue("pk"))), ui2.Toast(hv.ui2T(r, "apps.operation-completed"), false))
			return
		}
		a := ui2LoadAppsFor(ctx, r)
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

func ui2LoadCatalog(ctx *httpCtx, pk string) ui2.AppCatalog {
	entries, err := ctx.API.AppCatalog()
	c := ui2.CatalogOf(pk, entries)
	if err != nil {
		c.Err = err.Error()
	}
	return c
}

// ui2Catalog serves the catalog view, or with body set only the polled part.
func (hv *Hypervisor) ui2Catalog(body bool) http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		c := ui2LoadCatalog(ctx, r.PathValue("pk"))
		if body {
			hv.render(w, r, ui2.CatalogBody(c))
			return
		}
		hv.render(w, r, ui2.CatalogPage(c))
	})
}

// ui2CatalogAdd adds an instance of a catalog app, named by the prompt's
// answer when another instance was asked for.
func (hv *Hypervisor) ui2CatalogAdd() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		name := strings.TrimSpace(r.FormValue("name"))
		if p := strings.TrimSpace(r.Header.Get("HX-Prompt")); p != "" {
			name = p
		}
		body := map[string]string{"name": name, "binary": r.FormValue("binary")}
		if !ui2CallAPI(w, r, hv.postApp(), body) {
			return
		}
		c := ui2LoadCatalog(ctx, r.PathValue("pk"))
		hv.render(w, r, ui2.CatalogBody(c), ui2.Toast(hv.ui2T(r, "apps.operation-completed"), false))
	})
}

func (hv *Hypervisor) ui2AppFlags() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		f, err := ctx.API.AppFlags(r.PathValue("app"))
		v := ui2.FlagsViewOf(r.PathValue("pk"), f)
		v.App = r.PathValue("app")
		if err != nil {
			v.Err = err.Error()
		}
		hv.render(w, r, ui2.AppFlagsForm(v))
	})
}

// ui2AppFlagsSave checks each changed flag as the Angular flags form does
// and writes the rebuilt args through the JSON app handler. A flag counts
// as set when its value differs from the default.
func (hv *Hypervisor) ui2AppFlagsSave() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		if err := r.ParseForm(); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		name, pk := r.PathValue("app"), r.PathValue("pk")
		schema, err := ctx.API.AppFlags(name)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		st, err := ctx.API.App(name)
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
			return
		}
		values, set := map[string]string{}, map[string]bool{}
		v := ui2.AppFlagsView{PK: pk, App: name}
		bad := false
		for _, f := range schema.Flags {
			val := strings.TrimSpace(r.PostForm.Get("v:" + f.Name))
			if f.Type == visorapi.FlagTypeBool {
				val = strconv.FormatBool(val == "true")
			}
			row := ui2.FlagRow{AppFlag: f}
			row.Value = val
			values[f.Name], set[f.Name] = val, val != ui2.FlagDefault(f)
			row.Set = set[f.Name]
			if set[f.Name] {
				row.Err = ui2FlagErr(f, val)
				bad = bad || row.Err != ""
			}
			v.Flags = append(v.Flags, row)
		}
		if bad {
			v.Err = hv.ui2T(r, "app-settings.flags-invalid")
			hv.render(w, r, ui2.AppFlagsForm(v))
			return
		}
		args := ui2.BuildFlagArgs(st.Args, schema.Flags, values, set)
		if cw := callJSON(w, r, hv.putApp(), map[string]any{"args": ui2.ArgsString(args)}); cw.code != http.StatusOK {
			v.Err = cw.errText()
			hv.render(w, r, ui2.AppFlagsForm(v))
			return
		}
		if f, err := ctx.API.AppFlags(name); err == nil {
			v = ui2.FlagsViewOf(pk, f)
			v.App = name
		}
		hv.render(w, r, ui2.AppFlagsForm(v), ui2.Toast(hv.ui2T(r, "app-settings.saved"), false))
	})
}

// ui2FlagErr is why val does not fit flag f, or "".
func ui2FlagErr(f visorapi.AppFlag, val string) string {
	if val == "" {
		return ""
	}
	if f.Type == visorapi.FlagTypeInt {
		if _, err := strconv.ParseInt(val, 10, 64); err != nil {
			return err.Error()
		}
	}
	if f.Check != "" {
		if err := autoconfigcmd.CheckValue(f.Check, val); err != nil {
			return err.Error()
		}
	}
	return ""
}

// ui2AppsBulk starts, stops or sets autostart on every ticked app, as the
// Angular list's selection actions do, one putApp call per app.
func (hv *Hypervisor) ui2AppsBulk() http.HandlerFunc {
	return hv.withCtx(hv.ui2VisorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		if err := r.ParseForm(); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		var body map[string]any
		switch r.PostForm.Get("action") {
		case "start":
			body = map[string]any{"status": statusStart}
		case "stop":
			body = map[string]any{"status": statusStop}
		case "auto-on":
			body = map[string]any{"autostart": true}
		case "auto-off":
			body = map[string]any{"autostart": false}
		default:
			httputil.WriteJSON(w, r, http.StatusBadRequest, errors.New("unknown action"))
			return
		}
		var errs []string
		for _, name := range r.PostForm["sel"] {
			if err := ui2CallFor(r, r.PathValue("pk"), name, hv.putApp(), body); err != nil {
				errs = append(errs, name+": "+err.Error())
			}
		}
		a := ui2LoadAppsFor(ctx, r)
		msg, bad := hv.ui2T(r, "apps.operation-completed"), false
		if len(errs) > 0 {
			msg, bad = strings.Join(errs, "; "), true
		}
		hv.render(w, r, ui2.AppsRows(a), ui2.Toast(msg, bad))
	})
}
