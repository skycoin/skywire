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
	"bufio"
	"bytes"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
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
			writeHTML(w, r.URL.Path, "prose", proseIndex())
			return
		}
		b, err := fs.ReadFile(skydocs.Prose(), name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		writeHTML(w, r.URL.Path, name, mdToHTML(b))
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var pages []page
		collect(root, nil, &pages)

		// URL path -> page. page.path() is the FILE layout ("cli/dmsg/cat/
		// README.md") and the links render() emits use it verbatim, so the URL
		// carries the README.md too. Trimming it here means the file layout IS
		// the URL layout, and the file generator's contract stays untouched.
		//
		// It also makes every command a DIRECTORY as far as the browser is
		// concerned, which is what lets those relative links resolve: from
		// /cli/README.md a link to dmsg/README.md is /cli/dmsg/README.md.
		// Without the suffix the same link would land at /dmsg/README.md.
		want := strings.Trim(strings.TrimSuffix(strings.Trim(r.URL.Path, "/"), "README.md"), "/")
		for i := range pages {
			if strings.Join(pages[i].segs, "/") == want {
				var md bytes.Buffer
				if err := render(&md, pages[i]); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				writeHTML(w, r.URL.Path, pages[i].title(), mdToHTML(md.Bytes()))
				return
			}
		}
		http.NotFound(w, r)
	})

	return mux
}

// proseTitle reads the first markdown heading of a doc, which is what a
// reader recognizes — "Skywire Deployment on Kubernetes" rather than
// KUBERNETES_DEPLOYMENT.md. Falls back to the file name when a doc opens
// with something else, so a missing heading costs a nicer label and not the
// entry itself. Reads only the head of the file: the title is in the first
// few lines or it is not there.
func proseTitle(fsys fs.FS, p string) string {
	f, err := fsys.Open(p)
	if err != nil {
		return path.Base(p)
	}
	defer f.Close() //nolint:errcheck
	sc := bufio.NewScanner(io.LimitReader(f, 8<<10))
	for n := 0; sc.Scan() && n < 40; n++ {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "# ") {
			t := strings.TrimSpace(strings.TrimPrefix(line, "# "))
			if t != "" {
				return t
			}
		}
	}
	return path.Base(p)
}

// Section labels for the prose that sits at the top of docs/ with no directory
// of its own. They are labels, not paths: each link still points at the file
// where it actually lives, so nothing moves and no existing link breaks.
const (
	secReference = "reference"
	secRFC       = "rfcs and proposals"
)

// rfcName matches prose whose file name says it is a proposal rather than a
// description of what the code does. Both spellings are in use.
var rfcName = regexp.MustCompile("[-_]rfc[.]md$")

// proseIndex lists the embedded prose, grouped by the directory it lives in
// and titled by its first heading.
//
// It was a flat alphabetical list of file names, on the reasoning that the
// whole set fits on one page and a reader knows the name of the file they
// want. That holds for someone who already knows; for everyone else a
// hundred entries reading apps-overview.md through vpn-server.md is a
// directory listing, not documentation — and this is the page the desk shows
// in its browser, where it stands in for a full docs site.
func proseIndex() []byte {
	fsys := skydocs.Prose()
	bySection := map[string][]string{}
	var sections []string
	walkErr := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		sec := path.Dir(p)
		if sec == "." {
			// The top level holds a third of the prose — 33 files against
			// ~68 in sections — so leaving it as one unlabeled run put all
			// of them in a single wall above everything else. That is the
			// shape that still read as a directory listing even after the
			// per-directory grouping.
			//
			// A proposal and a description of what the code does are
			// different things to a reader looking for one of them, and the
			// file names already say which is which. Splitting on that beats
			// a curated list, which would go stale the first time someone
			// adds a file.
			if rfcName.MatchString(path.Base(p)) {
				sec = secRFC
			} else {
				sec = secReference
			}
		}
		if _, seen := bySection[sec]; !seen {
			sections = append(sections, sec)
		}
		bySection[sec] = append(bySection[sec], p)
		return nil
	})
	// Alphabetical, which puts "reference" before "rfcs and proposals" and both
	// among the directory sections. No bucket is privileged: the top level is
	// now two labeled sections like any other rather than an unlabeled run.
	sort.Strings(sections)
	var b strings.Builder
	b.WriteString("<h1>prose</h1>")
	for _, sec := range sections {
		names := bySection[sec]
		sort.Slice(names, func(i, j int) bool {
			return strings.ToLower(proseTitle(fsys, names[i])) < strings.ToLower(proseTitle(fsys, names[j]))
		})
		if sec != "" {
			fmt.Fprintf(&b, "<h2>%s</h2>", html.EscapeString(sec))
		}
		b.WriteString("<ul>")
		for _, n := range names {
			fmt.Fprintf(&b, "<li><a href=%q>%s</a> <small>%s</small></li>",
				n, html.EscapeString(proseTitle(fsys, n)), html.EscapeString(path.Base(n)))
		}
		b.WriteString("</ul>")
	}
	// The walk cannot fail over an embedded FS, but a truncated index that
	// says nothing is the one outcome worth ruling out: a reader would take
	// a short list for the whole of the prose.
	if walkErr != nil {
		fmt.Fprintf(&b, "<p>index incomplete: %s</p>", html.EscapeString(walkErr.Error()))
	}
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

// docPage is the whole document, hoisted to a constant so the Fprintf that
// emits it is one line — a raw string spanning ten lines leaves nowhere to
// put the directive that says why its error is dropped.
const docPage = `<!doctype html><meta charset=utf-8>
<meta name=viewport content="width=device-width,initial-scale=1">
<title>%s</title><style>
body{max-width:46em;margin:2em auto;padding:0 1em;font:15px/1.6 system-ui,sans-serif;color:#cdd2da;background:#15131c}
a{color:#9d7cff}code{background:#221d2e;padding:.1em .3em;border-radius:3px}
pre{background:#221d2e;padding:1em;overflow-x:auto;border-radius:4px}
pre code{background:none;padding:0}
table{border-collapse:collapse}td,th{border:1px solid #3a3350;padding:.3em .6em}
nav{margin-bottom:2em;font-size:13px}
</style><nav><a href="%[2]s">command reference</a> · <a href="%[2]sprose/">prose</a></nav>
%[3]s`

// relRoot is the path back to the site root FROM the directory the given URL
// path lives in — "" at the root, "../" one down, and so on.
//
// The nav has to be spelled relatively because this server does not know
// where it is mounted. In the browser visor it is reached through the vnet
// service worker at /<base>/vnet/<port>/, a prefix stripped long before the
// request arrives, so an absolute href="/prose/" leaves the desk entirely:
// on the docs site it resolved to https://skycoin.github.io/prose/ and got
// GitHub's "Site not found" page. Measured by clicking it.
func relRoot(urlPath string) string {
	// A browser resolves a relative href against the URL's DIRECTORY, which is
	// the URL itself when it ends in a slash and its parent when it does not.
	// Getting this wrong by one level is the whole bug being fixed, so it is
	// spelled out rather than folded into path.Dir.
	base := urlPath
	if !strings.HasSuffix(base, "/") {
		base = path.Dir(base)
	}
	if base = strings.Trim(base, "/"); base == "" || base == "." {
		return "./"
	}
	return strings.Repeat("../", strings.Count(base, "/")+1)
}

// writeHTML wraps rendered markdown in a minimal document. No external assets:
// this is served on a loopback with no transport behind it, so a stylesheet
// from a CDN would simply never arrive.
//
// The title is ESCAPED: for a prose page it is the file name taken off the
// request path, so it is request-controlled even though the file has to exist
// in the embedded FS for the handler to get this far. relRoot needs no
// escaping — it only ever returns "./" or a repetition of "../", never any
// part of the input — and the body is goldmark output, already HTML.
func writeHTML(w http.ResponseWriter, urlPath, title string, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// A dropped error, deliberately: a write failure here is the client
	// hanging up mid-page. There is no second channel to report it on and
	// nothing to retry.
	fmt.Fprintf(w, docPage, html.EscapeString(title), relRoot(urlPath), body) //nolint:errcheck,gosec
}
