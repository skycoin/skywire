package visor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/proxystatus"
)

func TestMeshStatusSurface(t *testing.T) {
	const suffix = ".haltingstate.net"
	cases := []struct {
		host    string
		want    proxystatus.Surface
		matched bool
	}{
		{"status-skysocks.haltingstate.net", proxystatus.SurfaceSkysocks, true},
		{"status-dmsg.haltingstate.net", proxystatus.SurfaceDmsg, true},
		{"status-skynet.haltingstate.net", proxystatus.SurfaceSkynet, true},
		{"STATUS-SkySocks.haltingstate.net", proxystatus.SurfaceSkysocks, true},
		{"status-skysocks.haltingstate.net:8443", "", false}, // caller strips port; a raw :port here is not matched
		{"status-unknown.haltingstate.net", "", false},
		{"vhost.abcd.haltingstate.net", "", false},        // ordinary browse frame
		{"status-skysocks.x.haltingstate.net", "", false}, // multi-label, not wildcard-safe
		{"status-skysocks.example.com", "", false},        // wrong suffix
		{"status-skysocks", "", false},                    // no suffix
	}
	for _, c := range cases {
		got, ok := meshStatusSurface(c.host, suffix)
		if ok != c.matched || got != c.want {
			t.Errorf("meshStatusSurface(%q) = (%q,%v), want (%q,%v)", c.host, got, ok, c.want, c.matched)
		}
	}
}

func TestMeshStatusSurfaceLocalhostSuffix(t *testing.T) {
	// The default (local) suffix also matches, so status works over http locally.
	if s, ok := meshStatusSurface("status-skysocks.mesh.localhost", ".mesh.localhost"); !ok || s != proxystatus.SurfaceSkysocks {
		t.Errorf("localhost suffix: got (%q,%v)", s, ok)
	}
}

type fixedStatus struct{}

func (fixedStatus) StatusSnapshot(s proxystatus.Surface) (proxystatus.Snapshot, error) {
	return proxystatus.Snapshot{Surface: s}, nil
}

// The status page is served whole at "/", and its live region alone at
// FragmentPath for a page that polls instead of holding the WebSocket.
func TestMeshStatusHandlerFragment(t *testing.T) {
	h := meshStatusHandler(".haltingstate.net", fixedStatus{}, http.NotFoundHandler())
	get := func(path string) string {
		r := httptest.NewRequest(http.MethodGet, "http://status-skysocks.haltingstate.net"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, w.Code)
		}
		return w.Body.String()
	}
	page, frag := get("/"), get(proxystatus.FragmentPath)
	if !strings.Contains(page, "<!doctype html>") || !strings.Contains(page, proxystatus.FragmentPath) {
		t.Fatal("page is not the full document with the polling fallback")
	}
	if strings.Contains(frag, "<!doctype") || frag == "" {
		t.Fatalf("fragment is not the live region alone: %.80q", frag)
	}
}
