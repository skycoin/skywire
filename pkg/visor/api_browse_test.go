// Package visor pkg/visor/api_browse_test.go c3-vis-api
package visor

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestApplyBrowseHeaders pins what a caller's headers may and may not set on a
// browse fetch. The allow case is the one the real-origin browser depends on:
// without Content-Type a form POST arrives as something the origin rejects.
func TestApplyBrowseHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://example.test/submit", strings.NewReader("a=1"))
	if err != nil {
		t.Fatal(err)
	}
	applyBrowseHeaders(req, map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"Accept":       "text/html",
		// Hop-by-hop: these describe the caller's connection, not the new one
		// the visor is about to open.
		"Connection":        "keep-alive",
		"Transfer-Encoding": "chunked",
		"Upgrade":           "websocket",
		"Proxy-Connection":  "keep-alive",
		"TE":                "trailers",
		"Trailer":           "Expires",
		"Keep-Alive":        "timeout=5",
		// net/http derives this from the body; a stale value truncates or hangs.
		"Content-Length": "999999",
		// Host is not a header here — net/http takes it from the URL.
		"Host": "evil.test",
	})
	if got := req.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want the form encoding", got)
	}
	if got := req.Header.Get("Accept"); got != "text/html" {
		t.Errorf("Accept = %q, want text/html", got)
	}
	for _, drop := range []string{
		"Connection", "Transfer-Encoding", "Upgrade", "Proxy-Connection",
		"TE", "Trailer", "Keep-Alive", "Content-Length", "Host",
	} {
		if got := req.Header.Get(drop); got != "" {
			t.Errorf("%s survived as %q; hop-by-hop and length headers must be dropped", drop, got)
		}
	}
	if req.Host == "evil.test" {
		t.Error("a caller-supplied Host header must not redirect the request")
	}
}

// TestBrowseFetchCarriesHeaders pins that the MESH legs carry a caller's
// headers under the same policy as the clearnet ones. Before this they did
// not: BrowseFetchRequest had no header field at all, so a form POST to a
// dmsg-hosted site arrived with no Content-Type, and SkynetHTTP Set every
// name it was handed including the hop-by-hop ones.
func TestBrowseFetchCarriesHeaders(t *testing.T) {
	// The field exists and round-trips as JSON — the wire the desk posts on.
	var req BrowseFetchRequest
	if err := json.Unmarshal([]byte(`{"host":"x.dmsg","port":80,"method":"POST","path":"/f",`+
		`"header":{"Content-Type":"application/x-www-form-urlencoded","Connection":"keep-alive"}}`), &req); err != nil {
		t.Fatalf("BrowseFetchRequest does not accept a header field: %v", err)
	}
	if req.Header["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("header did not survive the wire: %#v", req.Header)
	}

	// And the policy applied to it is the shared one, so a mesh POST cannot
	// smuggle a hop-by-hop header the clearnet legs would have dropped.
	httpReq, err := http.NewRequest(http.MethodPost, "http://x.dmsg/f", strings.NewReader("a=1"))
	if err != nil {
		t.Fatal(err)
	}
	applyBrowseHeaders(httpReq, req.Header)
	if got := httpReq.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want the form encoding", got)
	}
	if got := httpReq.Header.Get("Connection"); got != "" {
		t.Errorf("Connection survived as %q on the mesh leg", got)
	}
}
