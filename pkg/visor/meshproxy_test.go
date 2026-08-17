// Package visor pkg/visor/meshproxy_test.go c4-vis-mesh
package visor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalizeMeshSuffix(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"empty defaults to the loopback origin", "", defaultMeshProxySuffix},
		{"bare domain gains the leading dot", "example.net", ".example.net"},
		{"leading dot is preserved", ".example.net", ".example.net"},
		{"a lone dot is already dotted", ".", "."},
		{"multi-label domain", "browse.example.net", ".browse.example.net"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeMeshSuffix(tt.in); got != tt.want {
				t.Errorf("normalizeMeshSuffix(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHostWithoutPort(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"host and port", "site.mesh.localhost:8461", "site.mesh.localhost"},
		{"host only", "site.mesh.localhost", "site.mesh.localhost"},
		{"ipv6 with port is unbracketed", "[::1]:8461", "::1"},
		// No port means SplitHostPort fails and the input is returned as-is;
		// a bare bracketed IPv6 literal therefore keeps its brackets.
		{"bare ipv6 literal", "[::1]", "[::1]"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hostWithoutPort(tt.in); got != tt.want {
				t.Errorf("hostWithoutPort(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPeelNetLabel(t *testing.T) {
	tests := []struct {
		name, core, wantRest, wantNet string
	}{
		{"skynet label", "site.skynet", "site", "skynet"},
		{"dmsg label", "site.dmsg", "site", "dmsg"},
		{"no label", "site", "site", ""},
		{"label must be a whole final component", "site.dmsgx", "site.dmsgx", ""},
		{"a bare label peels to empty", "dmsg", "dmsg", ""},
		{"only the final label is peeled", "a.skynet.dmsg", "a.skynet", "dmsg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rest, network := peelNetLabel(tt.core)
			if rest != tt.wantRest || network != tt.wantNet {
				t.Errorf("peelNetLabel(%q) = (%q, %q), want (%q, %q)",
					tt.core, rest, network, tt.wantRest, tt.wantNet)
			}
		})
	}
}

// rewriteMeshLocation is what keeps a redirect inside the browse-frame origin.
// Getting it wrong either strands the user on clearnet (under-rewriting) or
// rewrites a genuinely external redirect into a bogus same-origin path.
func TestRewriteMeshLocation(t *testing.T) {
	const frameHost = "site.mesh.localhost:8461"
	const vhost = "site.example"

	tests := []struct {
		name     string
		status   int
		location string
		want     string
	}{
		{"relative location is normalised but stays relative", 302, "/inner", "/inner"},
		{"absolute location on the upstream vhost becomes relative", 302, "http://site.example/inner", "/inner"},
		{"query is preserved when made relative", 302, "http://site.example/inner?a=1&b=2", "/inner?a=1&b=2"},
		{"empty path becomes root", 302, "http://site.example", "/"},
		{"vhost match is case-insensitive", 302, "http://SITE.EXAMPLE/x", "/x"},
		{"a genuinely different host is left alone", 302, "http://elsewhere.example/x", "http://elsewhere.example/x"},
		{"non-redirect status is left alone", 200, "http://site.example/inner", "http://site.example/inner"},
		{"400 is not a redirect", 400, "http://site.example/inner", "http://site.example/inner"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tt.status, Header: http.Header{}}
			resp.Header.Set("Location", tt.location)
			rewriteMeshLocation(resp, frameHost, vhost)
			if got := resp.Header.Get("Location"); got != tt.want {
				t.Errorf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInterstitialEligible(t *testing.T) {
	newReq := func(method string, hdr map[string]string) *http.Request {
		r, err := http.NewRequest(method, "http://abc:80/", nil)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		return r
	}
	tests := []struct {
		name   string
		method string
		hdr    map[string]string
		want   bool
	}{
		{"document navigation", http.MethodGet, map[string]string{"Sec-Fetch-Dest": "document"}, true},
		{"image subresource", http.MethodGet, map[string]string{"Sec-Fetch-Dest": "image"}, false},
		{"script subresource", http.MethodGet, map[string]string{"Sec-Fetch-Dest": "script"}, false},
		{"empty dest (xhr/fetch)", http.MethodGet, map[string]string{"Sec-Fetch-Dest": "empty"}, false},
		{"no fetch-metadata, html accept", http.MethodGet, map[string]string{"Accept": "text/html,application/xhtml+xml"}, true},
		{"no fetch-metadata, non-html accept", http.MethodGet, map[string]string{"Accept": "image/png"}, false},
		{"Sec-Fetch-Dest wins over Accept", http.MethodGet, map[string]string{"Sec-Fetch-Dest": "image", "Accept": "text/html"}, false},
		{"POST is never eligible", http.MethodPost, map[string]string{"Sec-Fetch-Dest": "document"}, false},
		{"HEAD is never eligible", http.MethodHead, map[string]string{"Sec-Fetch-Dest": "document"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := interstitialEligible(newReq(tt.method, tt.hdr)); got != tt.want {
				t.Errorf("interstitialEligible = %v, want %v", got, tt.want)
			}
		})
	}
}

// gateRT is a stub next-RoundTripper: it records each call's context and either
// returns immediately (fast) or blocks on an explicit release channel, so tests
// can drive the cold / warm timing of meshInterstitialRT deterministically. It
// blocks on release (NOT the request context) precisely so a test can prove the
// detached upstream context outlives the inbound request's cancellation.
type gateRT struct {
	fast    bool
	err     error
	release chan struct{}

	mu     sync.Mutex
	calls  int
	lastCt context.Context
}

func (g *gateRT) RoundTrip(req *http.Request) (*http.Response, error) {
	g.mu.Lock()
	g.calls++
	g.lastCt = req.Context()
	fast := g.fast
	g.mu.Unlock()
	if !fast {
		<-g.release
	}
	if g.err != nil {
		return nil, g.err
	}
	return &http.Response{
		StatusCode: http.StatusOK, Status: "200 OK", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{}, Body: io.NopCloser(strings.NewReader("REAL-CONTENT")), ContentLength: 12,
		Request: req,
	}, nil
}

func (g *gateRT) numCalls() int { g.mu.Lock(); defer g.mu.Unlock(); return g.calls }
func (g *gateRT) ctx() context.Context {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastCt
}

// meshCtxReq builds a browse request carrying the per-request mesh context values
// Rewrite would have set, targeting a fixed destination key.
func meshCtxReq(t *testing.T, base context.Context, netLabel string, eligible bool) *http.Request {
	t.Helper()
	ctx := context.WithValue(base, meshNetKey, netLabel)
	ctx = context.WithValue(ctx, meshFrameHostKey, "site.mesh.localhost:8461")
	ctx = context.WithValue(ctx, meshVhostKey, "site.example")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://deadbeef:80/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if eligible {
		req.Header.Set("Sec-Fetch-Dest", "document")
	} else {
		req.Header.Set("Sec-Fetch-Dest", "image")
	}
	return req
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close() //nolint:errcheck
	return string(b)
}

// A cold, eligible request whose upstream blocks past the soft deadline must get
// the transient (auto-refreshing) interstitial fast, while the underlying dial
// keeps running on a context that is NOT canceled by the inbound request — the
// property that lets the route warm and persist for the browser's next reload.
func TestInterstitialRTColdServesTransient(t *testing.T) {
	g := &gateRT{release: make(chan struct{})} // blocks until released
	defer close(g.release)                     // let the warm-up dial finish at teardown
	rt := newMeshInterstitialRT(g, 30*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	req := meshCtxReq(t, ctx, "dmsg", true)

	start := time.Now()
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cold request took %v, expected ~soft deadline", elapsed)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	for _, want := range []string{"Building a route", `http-equiv="refresh"`, "site.example"} {
		if !strings.Contains(body, want) {
			t.Errorf("interstitial body missing %q", want)
		}
	}

	// A background warm-up dial was started, and it ran on a detached context that
	// the inbound cancel does not tear down.
	if g.numCalls() != 1 {
		t.Fatalf("warm-up dials = %d, want 1", g.numCalls())
	}
	cancel() // cancel the inbound request
	if dctx := g.ctx(); dctx.Err() != nil {
		t.Fatalf("detached upstream context was canceled by the inbound request: %v", dctx.Err())
	}
}

// Once a destination has answered, it is marked warm: subsequent navigations
// bypass the soft deadline and block for real content (a slow origin is waited
// out, not papered over with a refresh loop).
func TestInterstitialRTWarmBypasses(t *testing.T) {
	g := &gateRT{fast: true}
	rt := newMeshInterstitialRT(g, 30*time.Millisecond)

	req1 := meshCtxReq(t, context.Background(), "dmsg", true)
	resp1, err := rt.RoundTrip(req1)
	if err != nil {
		t.Fatalf("RoundTrip #1: %v", err)
	}
	if body := readBody(t, resp1); !strings.Contains(body, "REAL-CONTENT") {
		t.Fatalf("first response should be real content, got %q", body)
	}
	if !rt.isWarm(meshDestKey(req1)) {
		t.Fatal("destination should be marked warm after a successful round-trip")
	}

	req2 := meshCtxReq(t, context.Background(), "dmsg", true)
	resp2, err := rt.RoundTrip(req2)
	if err != nil {
		t.Fatalf("RoundTrip #2: %v", err)
	}
	if body := readBody(t, resp2); !strings.Contains(body, "REAL-CONTENT") {
		t.Fatalf("warm response should be real content, got %q", body)
	}
	if g.numCalls() != 2 {
		t.Fatalf("dials = %d, want 2 (both went to upstream)", g.numCalls())
	}
}

// A fast upstream error is classified: transient → auto-refresh page, hard → error
// page with a manual retry and no auto-refresh.
func TestInterstitialRTFailFastClassifies(t *testing.T) {
	t.Run("transient", func(t *testing.T) {
		g := &gateRT{fast: true, err: errors.New("dial dmsg: i/o timeout")}
		rt := newMeshInterstitialRT(g, time.Second)
		resp, err := rt.RoundTrip(meshCtxReq(t, context.Background(), "dmsg", true))
		if err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		body := readBody(t, resp)
		if !strings.Contains(body, `http-equiv="refresh"`) || strings.Contains(body, "Retry") {
			t.Errorf("transient error should auto-refresh without a retry button")
		}
	})
	t.Run("hard", func(t *testing.T) {
		g := &gateRT{fast: true, err: errors.New("skynet: bad dest key")}
		rt := newMeshInterstitialRT(g, time.Second)
		resp, err := rt.RoundTrip(meshCtxReq(t, context.Background(), "skynet", true))
		if err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("hard error status = %d, want 502", resp.StatusCode)
		}
		body := readBody(t, resp)
		if strings.Contains(body, `http-equiv="refresh"`) || !strings.Contains(body, "Retry") {
			t.Errorf("hard error should offer a manual retry and not auto-refresh")
		}
	})
}

// A resolve failure (meshErrKey) is a hard error rendered without ever dialing.
func TestInterstitialRTResolveError(t *testing.T) {
	g := &gateRT{fast: true}
	rt := newMeshInterstitialRT(g, time.Second)
	ctx := context.WithValue(context.Background(), meshErrKey, `host "x" has no suffix ".mesh.localhost"`)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://invalid.mesh/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Dest", "document")
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if g.numCalls() != 0 {
		t.Fatalf("resolve error must not dial upstream, got %d calls", g.numCalls())
	}
	if body := readBody(t, resp); !strings.Contains(body, "has no suffix") {
		t.Errorf("error page missing the resolve detail, got %q", body)
	}
}

// A non-eligible request (subresource) is never given the interstitial: it blocks
// for the real response even when cold.
func TestInterstitialRTSubresourceNeverInterstitial(t *testing.T) {
	g := &gateRT{fast: true}
	rt := newMeshInterstitialRT(g, 30*time.Millisecond)
	resp, err := rt.RoundTrip(meshCtxReq(t, context.Background(), "dmsg", false))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if body := readBody(t, resp); !strings.Contains(body, "REAL-CONTENT") {
		t.Fatalf("subresource should get real content, got %q", body)
	}
}

func TestRewriteMeshLocationNoops(t *testing.T) {
	t.Run("absent Location header stays absent", func(t *testing.T) {
		resp := &http.Response{StatusCode: 302, Header: http.Header{}}
		rewriteMeshLocation(resp, "site.mesh.localhost", "site.example")
		if _, ok := resp.Header["Location"]; ok {
			t.Error("a Location header was added where none existed")
		}
	})
	t.Run("empty frameHost disables rewriting", func(t *testing.T) {
		resp := &http.Response{StatusCode: 302, Header: http.Header{}}
		resp.Header.Set("Location", "http://site.example/inner")
		rewriteMeshLocation(resp, "", "site.example")
		if got := resp.Header.Get("Location"); got != "http://site.example/inner" {
			t.Errorf("Location = %q, want it untouched", got)
		}
	})
}
