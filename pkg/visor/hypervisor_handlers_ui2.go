// Package visor pkg/visor/hypervisor_handlers_ui2.go c3-vis-core
package visor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/ui2"
)

// The server rendered UI at /ui2/ (pkg/visor/ui2), beside the Angular one at
// "/". Its handlers read and write through the same ctx.API as the JSON
// routes, behind the same session and CSRF checks, so they work for the local
// visor and remote ones. Every URL they write is relative, so the pages also
// work under the /vnet/<port>/ prefix of the in-tab visor.

// ui2Catalog reads the Angular UI's translation files from its embedded build.
var ui2Catalog = sync.OnceValue(func() *ui2.Catalog {
	sub, err := fs.Sub(ui, "static")
	if err != nil {
		return ui2.NewCatalog(nil)
	}
	return ui2.NewCatalog(sub)
})

// ui2Routes registers the /ui2 pages. Static files and the login page are
// public, like the Angular bundle.
func (hv *Hypervisor) ui2Routes(r *httputil.Router) {
	r.Handle("/ui2/static/{file}", ui2Static())
	r.Get("/ui2", func(w http.ResponseWriter, _ *http.Request) { ui2Redirect(w, "ui2/") })
	r.Group(func(r *httputil.Router) {
		r.Use(httputil.CompressMin(httputil.CompressMinBytes, 5))
		r.Get("/ui2/login", hv.ui2LoginPage())
		r.Post("/ui2/login", hv.ui2LoginPost())
		r.Post("/ui2/logout", hv.ui2Logout())
	})
	r.Group(func(r *httputil.Router) {
		r.Use(hv.ui2Auth, httputil.CompressMin(httputil.CompressMinBytes, 5))
		r.Get("/ui2/", hv.ui2Nodes(false))
		r.Get("/ui2/nodes", hv.ui2Nodes(true))
		r.Get("/ui2/visors/{pk}", func(w http.ResponseWriter, r *http.Request) {
			ui2Redirect(w, r.PathValue("pk")+"/info")
		})
		r.Get("/ui2/visors/{pk}/{tab}", hv.ui2NotPorted())
		hv.ui2NodesRoutes(r)
		hv.ui2BulkRoutes(r)
		hv.ui2HVSettingsRoutes(r)
		hv.ui2ServicesRoutes(r)
		hv.ui2ResourcesRoutes(r)
		hv.ui2FleetUptimeRoutes(r)
		hv.ui2ContactsRoutes(r)
		hv.ui2AppsRoutes(r)
		hv.ui2TransportsRoutes(r)
		hv.ui2RoutingRoutes(r)
		hv.ui2LogsRoutes(r)
		hv.ui2MailRoutes(r)
		hv.ui2ChatRoutes(r)
		hv.ui2TerminalRoutes(r)
		hv.ui2WalletRoutes(r)
		hv.ui2InfoRoutes(r)
		hv.ui2UptimeRoutes(r)
		hv.ui2BandwidthRoutes(r)
		hv.ui2WebProxyRoutes(r)
		hv.ui2SkynetRoutes(r)
		hv.ui2RewardsRoutes(r)
		hv.ui2DmsgRoutes(r)
		hv.ui2SettingsRoutes(r)
	})
	// Without this an unknown /ui2 path would fall through to the Angular UI.
	r.HandleFunc("/ui2/{rest...}", http.NotFound)
}

// ui2Static serves the CSS, JS and htmx gzipped. A request naming the file's
// current content hash in ?v= may be cached for good.
func ui2Static() http.Handler {
	files := http.StripPrefix("/ui2/static/", http.FileServerFS(ui2.Static()))
	return httputil.CompressMin(httputil.CompressMinBytes, 5)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := ui2.AssetVersion(r.PathValue("file"))
		if v != "" {
			w.Header().Set("ETag", `W/"`+v+`"`)
		}
		if v != "" && r.URL.Query().Get("v") == v {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	}))
}

// ui2Redirect answers 303 with a relative Location. http.Redirect would make
// it absolute and so drop the /vnet/<port>/ prefix.
func ui2Redirect(w http.ResponseWriter, loc string) {
	w.Header().Set("Location", loc)
	w.WriteHeader(http.StatusSeeOther)
}

// ui2Auth sends a request without a session to the login page: a page load
// by redirect, an htmx request by HX-Redirect.
func (hv *Hypervisor) ui2Auth(next http.Handler) http.Handler {
	if !hv.c.EnableAuth || hv.users == nil {
		return next
	}
	authed := hv.users.Authorize(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hv.users.HasSession(r) {
			authed.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("HX-Request") != "" {
			w.Header().Set("HX-Redirect", "ui2/login")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		ui2Redirect(w, ui2.BaseFor(r.URL.Path)+"ui2/login")
	})
}

// ui2Ctx is the request context with the page settings the templates read.
func (hv *Hypervisor) ui2Ctx(r *http.Request) context.Context {
	p := ui2.Page{Base: ui2.BaseFor(r.URL.Path), HVPK: hv.c.PK.Hex(), TPViz: hv.tpvizServer != nil}
	p.Auth = hv.c.EnableAuth && hv.users != nil && hv.users.HasSession(r)
	var lang string
	if c, err := r.Cookie(ui2.LangCookie); err == nil {
		lang = c.Value
	}
	p.Lang = ui2.PickLang(lang, r.Header.Get("Accept-Language"))
	var refresh string
	if c, err := r.Cookie(ui2.RefreshCookie); err == nil {
		refresh = c.Value
	}
	p.Refresh = ui2.PickRefresh(refresh)
	return ui2.With(r.Context(), p, ui2Catalog())
}

// render writes the components in order, the first one the page or the
// fragment and any later ones out of band swaps.
func (hv *Hypervisor) render(w http.ResponseWriter, r *http.Request, cs ...templ.Component) {
	hv.renderStatus(w, r, http.StatusOK, cs...)
}

func (hv *Hypervisor) renderStatus(w http.ResponseWriter, r *http.Request, code int, cs ...templ.Component) {
	ctx := hv.ui2Ctx(r)
	var buf bytes.Buffer
	for _, c := range cs {
		if err := c.Render(ctx, &buf); err != nil {
			httputil.GetLogger(r).WithError(err).Warn("ui2: render")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(buf.Bytes()) //nolint:errcheck,gosec
}

// ui2T translates key for the request's language.
func (hv *Hypervisor) ui2T(r *http.Request, key string, args ...string) string {
	return ui2.T(hv.ui2Ctx(r), key, args...)
}

// captureWriter takes a JSON handler's status and body while its headers,
// such as a session cookie, go out on the real response.
type captureWriter struct {
	h    http.Header
	code int
	body bytes.Buffer
}

func (c *captureWriter) Header() http.Header         { return c.h }
func (c *captureWriter) Write(p []byte) (int, error) { return c.body.Write(p) }
func (c *captureWriter) WriteHeader(code int)        { c.code = code }

// errText is the message of a {"error": "..."} body, or the body itself.
func (c *captureWriter) errText() string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(c.body.Bytes(), &e) == nil && e.Error != "" {
		return e.Error
	}
	return http.StatusText(c.code)
}

// callJSON runs a JSON handler with v as the request body.
func callJSON(w http.ResponseWriter, r *http.Request, h http.Handler, v any) *captureWriter {
	b, _ := json.Marshal(v) //nolint:errcheck
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(b))
	r2.ContentLength = int64(len(b))
	r2.Header.Set("Content-Type", "application/json")
	cw := &captureWriter{h: w.Header(), code: http.StatusOK}
	h.ServeHTTP(cw, r2)
	w.Header().Del("Content-Type")
	return cw
}

func (hv *Hypervisor) ui2LoginPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !hv.c.EnableAuth || hv.users == nil || hv.users.HasSession(r) {
			ui2Redirect(w, "./")
			return
		}
		hv.render(w, r, ui2.LoginPage(!hv.users.HasUser(), ""))
	}
}

// ui2LoginPost logs in through the same handlers as /api/login and
// /api/create-account, from a plain form post.
func (hv *Hypervisor) ui2LoginPost() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !hv.c.EnableAuth || hv.users == nil || hv.users.HasSession(r) {
			ui2Redirect(w, "./")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		creds := struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}{r.PostForm.Get("username"), r.PostForm.Get("password")}
		create := r.PostForm.Get("create") != "" && !hv.users.HasUser()
		fail := func(msg string) {
			hv.renderStatus(w, r, http.StatusUnauthorized, ui2.LoginPage(create, msg))
		}
		if create {
			if creds.Password != r.PostForm.Get("confirm") {
				fail(hv.ui2T(r, "ui2.password-mismatch"))
				return
			}
			if cw := callJSON(w, r, hv.users.CreateAccount(), creds); cw.code != http.StatusOK {
				fail(cw.errText())
				return
			}
		}
		cw := callJSON(w, r, hv.users.Login(), creds)
		switch {
		case cw.code == http.StatusUnauthorized:
			fail(hv.ui2T(r, "login.incorrect-password"))
		case cw.code != http.StatusOK:
			fail(cw.errText())
		default:
			ui2Redirect(w, "./")
		}
	}
}

func (hv *Hypervisor) ui2Logout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hv.c.EnableAuth && hv.users != nil {
			callJSON(w, r, hv.users.Logout(), nil)
		}
		if r.Header.Get("HX-Request") != "" {
			w.Header().Set("HX-Redirect", "ui2/login")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		ui2Redirect(w, "login")
	}
}

// ui2Nodes serves the visor list page, or with part set only the table the
// page polls. A visor that runs its own hypervisor lists that hypervisor's
// visors under its row, as the Angular list does.
func (hv *Hypervisor) ui2Nodes(part bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The sub-hypervisor walk can take longer than the server's write
		// timeout, as for /api/visors-tree-summary.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(45 * time.Second)) //nolint:errcheck
		ns := ui2NodesOf(hv.visorTree())
		if part {
			hv.render(w, r, ui2.NodeRows(ns, "", true))
			return
		}
		hv.render(w, r, ui2.NodesPage(ns, ""))
	}
}

// ui2NodesOf turns the tree into rows: the local section, each with the
// section of the sub-hypervisor it runs, if any, nested under it.
func ui2NodesOf(secs []VisorTreeSection) []ui2.Node {
	if len(secs) == 0 {
		return nil
	}
	ns := make([]ui2.Node, 0, len(secs[0].Visors))
	direct := map[string]bool{}
	for _, s := range secs[0].Visors {
		n := ui2.NodeOf(s)
		direct[n.PK] = true
		ns = append(ns, n)
	}
	sub := map[string]VisorTreeSection{}
	for _, s := range secs[1:] {
		sub[s.HypervisorPK.Hex()] = s
	}
	for i := range ns {
		s, ok := sub[ns[i].PK]
		if !ok {
			continue
		}
		ns[i].SubErr = s.SubError
		for _, v := range s.Visors {
			c := ui2.NodeOf(v)
			if c.PK == ns[i].PK {
				continue
			}
			c.ViaSub, c.IsHV = !direct[c.PK], false
			ns[i].Children = append(ns[i].Children, c)
		}
		ui2.SortNodes(ns[i].Children)
	}
	ui2.SortNodes(ns)
	ui2.MarkOutdated(ns)
	return ns
}

// ui2NotPorted answers a visor tab the new UI does not have yet with a link
// to the classic one.
func (hv *Hypervisor) ui2NotPorted() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var pk cipher.PubKey
		if err := pk.Set(r.PathValue("pk")); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		if _, ok := ui2.TabOf(r.PathValue("tab")); !ok {
			http.NotFound(w, r)
			return
		}
		hv.render(w, r, ui2.NotPorted(pk.Hex(), r.PathValue("tab")))
	}
}

// ui2VisorCtx is visorCtx for the pages: a full page load for a visor that
// cannot be reached gets a page saying so instead of a JSON error.
func (hv *Hypervisor) ui2VisorCtx(w http.ResponseWriter, r *http.Request) (*httpCtx, bool) {
	if r.Method != http.MethodGet || r.Header.Get("HX-Request") != "" {
		return hv.visorCtx(w, r)
	}
	cw := &captureWriter{h: w.Header(), code: http.StatusOK}
	ctx, ok := hv.visorCtx(cw, r)
	if ok {
		return ctx, true
	}
	w.Header().Del("Content-Type")
	hv.renderStatus(w, r, cw.code, ui2.VisorError(r.PathValue("pk"), ui2TabSeg(r), cw.errText()))
	return nil, false
}

// ui2TabSeg is the tab segment of a /ui2/visors/{pk}/{tab}/... path.
func ui2TabSeg(r *http.Request) string {
	if t := r.PathValue("tab"); t != "" {
		return t
	}
	if pk := r.PathValue("pk"); pk != "" {
		if _, rest, ok := strings.Cut(r.URL.Path, pk+"/"); ok {
			seg, _, _ := strings.Cut(rest, "/")
			return seg
		}
	}
	return "info"
}
