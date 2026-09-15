// Package doc cmd/skywire/commands/doc/serve_test.go c1-cli-doc
package doc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestRelRoot. Every nav href is built from this, and the failure it exists to
// prevent is silent: an href one level off still renders, it just navigates
// somewhere else. The docs site shipped with absolute hrefs and every one of
// them left the desk — clicking "prose" reached GitHub's "Site not found".
func TestRelRoot(t *testing.T) {
	for _, c := range []struct{ url, want string }{
		{"/", "./"},
		{"/prose/", "../"},
		{"/prose/README.md", "../"},
		{"/prose/guides/pairing.md", "../../"},
		{"/cli/README.md", "../"},
		{"/cli/dmsg/cat/README.md", "../../../"},
	} {
		if got := relRoot(c.url); got != c.want {
			t.Errorf("relRoot(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// TestCommandURLAcceptsFileLayout. render() emits links in the FILE layout,
// README.md and all, so the server has to answer at that URL — and it must be
// that URL, not the bare command, or the relative links on the page resolve
// one level too high (from /cli, "dmsg/README.md" is /dmsg/README.md).
func TestCommandURLAcceptsFileLayout(t *testing.T) {
	root := &cobra.Command{Use: "skywire"}
	root.AddCommand(&cobra.Command{Use: "cli", Short: "cli", Run: func(*cobra.Command, []string) {}})
	h := docHandler(root)
	for _, u := range []string{"/cli/README.md", "/cli/", "/cli"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
		if rec.Code != http.StatusOK && rec.Code != http.StatusMovedPermanently {
			t.Errorf("GET %s = %d, want 200 (or a redirect to one)", u, rec.Code)
		}
	}
}

// TestNoAbsoluteNavHrefs. The server has no idea what prefix it is mounted
// under — through the vnet service worker it is /<base>/vnet/<port>/, stripped
// before the request arrives. A single leading-slash href puts the reader back
// on the host origin.
func TestNoAbsoluteNavHrefs(t *testing.T) {
	root := &cobra.Command{Use: "skywire"}
	root.AddCommand(&cobra.Command{Use: "cli", Short: "cli", Run: func(*cobra.Command, []string) {}})
	h := docHandler(root)
	for _, u := range []string{"/", "/cli/README.md", "/prose/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
		if strings.Contains(rec.Body.String(), `href="/`) {
			t.Errorf("GET %s emitted an absolute href — it would leave the desk", u)
		}
	}
}

// TestTitleIsEscaped. A prose page's title is the file name taken off the
// request path — request-controlled, even though the file must exist in the
// embedded FS to get this far. It lands inside <title>, so it is escaped;
// CodeQL flagged the unescaped version as a reflected-XSS sink.
func TestTitleIsEscaped(t *testing.T) {
	rec := httptest.NewRecorder()
	writeHTML(rec, "/prose/x.md", `</title><script>alert(1)</script>`, []byte("body"))
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Error("writeHTML passed markup through the title unescaped")
	}
	if !strings.Contains(rec.Body.String(), "&lt;script&gt;") {
		t.Error("the title was not escaped")
	}
}
