// Package landingpage renders the visor landing page shown over dmsg/skynet at
// http://<pk>.dmsg/ (and skywire.dmsg → the visor's own PK).
//
// It is deliberately dependency-free (only html + strings) so BOTH the native
// visor's log-server (pkg/visor/logserver, net/http) and the browser wasm-visor
// (cmd/wasm-visor, js/wasm, net/http-free) render the IDENTICAL page — same title,
// styling and structure — instead of two divergent hand-rolled pages. Each caller
// supplies its own link list (the native visor exposes more, gated by whitelist;
// the wasm-visor a minimal set), but the frame is shared.
package landingpage

import (
	"html"
	"strings"
)

// Render returns the full landing-page HTML for a visor with the given public key
// (hex; may be empty) and the given pre-formatted link lines. Each link line is
// emitted verbatim inside a <pre> block, so callers format them as they like, e.g.
// `<a href="/health">/health</a> - visor health status`.
func Render(pk string, links []string) string {
	var idHeader string
	if pk != "" {
		idHeader = `<p class="pk">public key: ` + html.EscapeString(pk) + `</p>`
	}
	return `<!doctype html><html><head><title>Skywire Visor</title>` +
		`<style>body{background:#000;color:#fff;font-family:monospace;padding:20px}a{color:#3399FF}a:visited{color:#FF00FF}` +
		`.pk{color:#5fd75f;word-break:break-all;margin:2px 0}</style>` +
		`</head><body><h2>Skywire Visor</h2>` + idHeader +
		`<pre>` + strings.Join(links, "\n") + `</pre></body></html>`
}
