// Package ui2 pkg/visor/ui2/ui2.go c3-vis-core
//
// Package ui2 is a server rendered hypervisor UI built with templ and htmx.
// It only renders. The hypervisor handlers fetch the data from the visor API
// and hand it here, so the package carries no visor dependency.
//
// Never use html/template or text/template here: they panic under TinyGo.
package ui2

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"strings"
)

//go:generate go run github.com/a-h/templ/cmd/templ@v0.3.1070 generate

// HTMXVersion is the vendored static/htmx.min.js, checked against the npm
// tarball integrity of htmx.org@2.0.11.
const HTMXVersion = "2.0.11"

//go:embed static
var static embed.FS

var staticFS = func() fs.FS {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	return sub
}()

// assetVer maps each static file to a short hash of its content, so a page
// can name a URL that changes whenever the file does.
var assetVer = func() map[string]string {
	m := map[string]string{}
	_ = fs.WalkDir(staticFS, ".", func(p string, d fs.DirEntry, err error) error { //nolint:errcheck
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(staticFS, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		m[p] = hex.EncodeToString(sum[:6])
		return nil
	})
	return m
}()

// Static is the CSS, JS and htmx the pages load, rooted at static/.
func Static() fs.FS { return staticFS }

// AssetVersion is the content hash of a static file, or "" if there is none.
func AssetVersion(name string) string { return assetVer[name] }

// Asset is the URL of a static file, relative to the page's <base>, carrying
// its content hash so it can be cached for good.
func Asset(name string) string {
	return "ui2/static/" + name + "?v=" + assetVer[name]
}

// BaseFor is the relative path from the request path back to the root. Every
// URL in a page is relative to it, so pages keep working under the
// /vnet/<port>/ prefix the in-tab visor's service worker adds.
func BaseFor(path string) string {
	n := strings.Count(path, "/") - 1
	if n <= 0 {
		return "./"
	}
	return strings.Repeat("../", n)
}

// Page is what every page and fragment needs to render: where the root is,
// the language, and whether there is a session to log out of.
type Page struct {
	Base   string
	Lang   string
	Auth   bool
	HVPK   string
	Remote bool
	TPViz  bool
	// Refresh is how often, in seconds, the visor list polls.
	Refresh int
	cat     *Catalog
}

type pageKey struct{}

// With returns ctx carrying p, rendering with the catalog c.
func With(ctx context.Context, p Page, c *Catalog) context.Context {
	p.cat = c
	return context.WithValue(ctx, pageKey{}, &p)
}

// From is the Page in ctx, or an English page rooted at "./".
func From(ctx context.Context) *Page {
	if p, ok := ctx.Value(pageKey{}).(*Page); ok {
		return p
	}
	return &Page{Base: "./", Lang: "en"}
}

// T translates key into the page language. args are name, value pairs that
// fill {{ name }} placeholders, as ngx-translate does.
func T(ctx context.Context, key string, args ...string) string {
	p := From(ctx)
	s := p.cat.Get(p.Lang, key)
	for i := 0; i+1 < len(args); i += 2 {
		s = strings.ReplaceAll(s, "{{ "+args[i]+" }}", args[i+1])
		s = strings.ReplaceAll(s, "{{"+args[i]+"}}", args[i+1])
	}
	return s
}
