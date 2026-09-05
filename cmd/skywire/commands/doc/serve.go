// Package doc cmd/skywire/commands/doc/serve.go c1-cli-doc
// `skywire doc serve` — the documentation, served by the thing it documents.
//
// Two sources, one site:
//
//	/            the cobra tree, walked live by collect() — zero embedded
//	             bytes, and it cannot drift from the binary because it IS the
//	             binary's command tree
//	/prose/…     the hand-written markdown embedded by package docs
//
// Why this exists rather than a link to the docs site: in the browser visor
// the nested browser has no transport until someone starts a visor, so a
// clearnet fetch of the docs strands on a placeholder (measured live). Served
// on the virtual loopback this is a same-origin /vnet/<port>/ URL, which
// netscrape's DirectLoader already claims for NATIVE rendering — so the docs
// render with no visor running, no transport in the path, and the reader can
// keep a terminal beside them.
//
// It emits HTML, not markdown. Browsers do not render markdown — a native
// `skywire doc serve` is meant to be opened in an ordinary browser, and making
// netscrape grow a private content type would make these docs readable only
// inside skywire.
package doc

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	ghtml "github.com/yuin/goldmark/renderer/html"

	"github.com/0magnet/bottle/vnet"

	skydocs "github.com/skycoin/skywire/docs"
)

var serveAddr string

func init() {
	RootCmd.AddCommand(serveCmd)
	// --addr, matching the 144 other commands that spell it that way (only
	// four use --bind). The wording is the address-resolver's.
	serveCmd.Flags().StringVarP(&serveAddr, "addr", "a", ":8085", "address to bind to")
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the CLI reference and prose docs over HTTP",
	Long: `Serve this binary's own documentation.

The command reference is generated from the live cobra tree on every
request, so it always describes the binary serving it. The prose is the
embedded docs/ markdown. Both are rendered to HTML.

In the browser visor this binds the virtual loopback, so the nested
browser reaches it at /vnet/<port>/ — a same-origin URL it renders
natively, with no visor and no transport needed.

  skywire doc serve                    # :8085
  skywire doc serve --addr 127.0.0.1:9000`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// bottle/vnet, not net: a real socket natively, the page port table under
		// js. Binding with net directly would serve fine on a workstation and
		// bind nothing reachable in the browser — where the desk needs to open
		// this at /vnet/<port>/, which is the case the command exists for.
		ln, err := vnet.Listen("tcp", serveAddr)
		if err != nil {
			return fmt.Errorf("doc serve: listen %s: %w", serveAddr, err)
		}
		cmd.Printf("serving docs on http://%s\n", ln.Addr())
		return http.Serve(ln, docHandler(cmd.Root())) //nolint:gosec // long-lived docs server; no timeouts wanted on a loopback reader
	},
}

// docHandler builds the site. The cobra tree is walked once per request
// rather than cached: it is cheap, and a served page that disagrees with the
// binary would defeat the point of generating it.
func docHandler(root *cobra.Command) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/prose/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/prose/")
		if name == "" {
			writeHTML(w, "prose", proseIndex())
			return
		}
		b, err := fs.ReadFile(skydocs.Prose(), name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		writeHTML(w, name, mdToHTML(b))
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var pages []page
		collect(root, nil, &pages)

		// URL path -> page. page.path() is the FILE layout ("cli/dmsg/cat/
		// README.md"); the URL drops the README.md. Leaving path() alone keeps
		// the file generator's contract untouched.
		want := strings.Trim(r.URL.Path, "/")
		for i := range pages {
			if strings.Join(pages[i].segs, "/") == want {
				var md bytes.Buffer
				if err := render(&md, pages[i]); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				writeHTML(w, pages[i].title(), mdToHTML(md.Bytes()))
				return
			}
		}
		http.NotFound(w, r)
	})

	return mux
}

// proseIndex lists the embedded prose. A flat sorted list rather than a tree:
// 102 files fit on a page, and a reader looking for one knows its name.
func proseIndex() []byte {
	var names []string
	_ = fs.WalkDir(skydocs.Prose(), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			names = append(names, p)
		}
		return nil
	})
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("<h1>prose</h1><ul>")
	for _, n := range names {
		fmt.Fprintf(&b, "<li><a href=%q>%s</a></li>", "/prose/"+n, n)
	}
	b.WriteString("</ul>")
	return []byte(b.String())
}

// mdToHTML renders markdown the way `skywire cli reward rules --html` already
// does, so the two agree on what markdown means here.
func mdToHTML(src []byte) []byte {
	var buf bytes.Buffer
	md := goldmark.New(
		goldmark.WithExtensions(extension.Strikethrough, extension.Table),
		goldmark.WithRendererOptions(ghtml.WithUnsafe()),
	)
	if err := md.Convert(src, &buf); err != nil {
		return []byte("<pre>" + err.Error() + "</pre>")
	}
	return buf.Bytes()
}

// writeHTML wraps rendered markdown in a minimal document. No external assets:
// this is served on a loopback with no transport behind it, so a stylesheet
// from a CDN would simply never arrive.
func writeHTML(w http.ResponseWriter, title string, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset=utf-8>
<meta name=viewport content="width=device-width,initial-scale=1">
<title>%s</title><style>
body{max-width:46em;margin:2em auto;padding:0 1em;font:15px/1.6 system-ui,sans-serif;color:#cdd2da;background:#15131c}
a{color:#9d7cff}code{background:#221d2e;padding:.1em .3em;border-radius:3px}
pre{background:#221d2e;padding:1em;overflow-x:auto;border-radius:4px}
pre code{background:none;padding:0}
table{border-collapse:collapse}td,th{border:1px solid #3a3350;padding:.3em .6em}
nav{margin-bottom:2em;font-size:13px}
</style><nav><a href="/">command reference</a> · <a href="/prose/">prose</a></nav>
%s`, title, body)
}
