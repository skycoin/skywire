// Package commands cmd/skywire/commands/doc_links_test.go c1-cli-doc
package commands

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/skycoin/skywire/cmd/skywire/commands/doc"
	skydocs "github.com/skycoin/skywire/docs"
)

// mdLink matches a markdown link target. Reference-style definitions and bare
// autolinks are not used in this prose, so the inline form is the whole of it.
var mdLink = regexp.MustCompile(`]\(([^)]+)\)`)

// TestEveryProseLinkResolves walks the embedded prose, resolves every internal
// link the way a browser would, and asks the site for it.
//
// This is the test the docs did not have, and it lives HERE rather than beside
// the server because it needs the real command tree: a prose link into the
// generated reference ("../skywire/cli/config/README.md") is answered by
// resolving it against that tree, so a stub root cannot tell a dead link from
// a command the test never supplied.
//
// The three failures it was written against, all of which render as ordinary
// links and fail only when someone clicks: links into docs/skywire/, the
// generated reference, which is checked in for GitHub and not embedded here;
// links at directories, which GitHub serves as a listing and this did not; and
// links at docs that were never embedded at all.
//
// External links are skipped deliberately — this asserts the site is
// internally navigable, not that the internet is up.
func TestEveryProseLinkResolves(t *testing.T) {
	srv := httptest.NewServer(doc.Handler(RootCmd))
	defer srv.Close()

	var broken []string
	err := fs.WalkDir(skydocs.Prose(), ".", func(p string, d fs.DirEntry, err error) error {
		// Site chrome is not served, so its links are not this site's to keep.
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") || doc.SiteChrome(p) {
			return err
		}
		b, err := fs.ReadFile(skydocs.Prose(), p)
		if err != nil {
			return err
		}
		for _, m := range mdLink.FindAllStringSubmatch(string(b), -1) {
			target, frag := m[1], ""
			// The fragment is checked too, against the ids goldmark generates
			// from the headings. An href that names a heading which was renamed
			// lands at the top of the right page, silently — which is how a
			// whole page of anchors went stale under a section rename.
			if i := strings.IndexByte(target, '#'); i >= 0 {
				target, frag = target[:i], target[i+1:]
			}
			switch {
			case target == "": // a pure fragment — the same page
				continue
			case strings.Contains(target, "://"), strings.HasPrefix(target, "mailto:"):
				continue
			}
			url := srv.URL + "/prose/" + path.Join(path.Dir(p), target)
			if strings.HasPrefix(target, "/") {
				url = srv.URL + "/prose" + target
			}
			// A trailing slash is the difference between a document and a
			// directory listing, and path.Join eats it.
			if strings.HasSuffix(target, "/") {
				url += "/"
			}
			resp, err := http.Get(url) //nolint:gosec,noctx
			if err != nil {
				broken = append(broken, p+" -> "+m[1]+": "+err.Error())
				continue
			}
			body, _ := io.ReadAll(resp.Body) //nolint:errcheck
			_ = resp.Body.Close()            //nolint:errcheck
			if resp.StatusCode != http.StatusOK {
				broken = append(broken, p+" -> "+m[1]+" ("+resp.Status+")")
				continue
			}
			// Reference pages are reached through a redirect this client does
			// follow, but their anchors are generated from a command tree, not
			// from prose headings; only prose fragments are checked here.
			if frag != "" && !bytes.Contains(body, []byte(`id="`+frag+`"`)) {
				broken = append(broken, p+" -> "+m[1]+" (no such heading)")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk prose: %v", err)
	}
	sort.Strings(broken)
	for _, b := range broken {
		t.Errorf("dead link: %s", b)
	}
}
