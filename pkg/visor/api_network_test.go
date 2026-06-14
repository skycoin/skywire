package visor

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestStampSkywireIdentity verifies the anti-spoof contract of the
// inject_pk forward: a client-supplied X-Skywire-* header is overwritten
// (never trusted), the authentic PK + transport are set, other headers are
// untouched, and an unidentifiable peer leaves the PK header absent.
func TestStampSkywireIdentity(t *testing.T) {
	var pk cipher.PubKey
	if err := pk.Set("0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c"); err != nil {
		t.Fatalf("set pk: %v", err)
	}

	t.Run("authenticated overwrites a spoofed header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://backend/", nil)
		req.Header.Set("X-Skywire-Remote-PK", "deadbeef-forged-by-client") // spoof attempt
		req.Header.Set("X-Skywire-Transport", "lies")
		req.Header.Set("X-Other", "keep-me")

		stampSkywireIdentity(req, pk, true, "skynet")

		if got := req.Header.Get("X-Skywire-Remote-PK"); got != pk.Hex() {
			t.Errorf("PK = %q, want authentic %q", got, pk.Hex())
		}
		if got := req.Header.Get("X-Skywire-Transport"); got != "skynet" {
			t.Errorf("transport = %q, want skynet", got)
		}
		if got := req.Header.Get("X-Other"); got != "keep-me" {
			t.Errorf("unrelated header dropped: %q", got)
		}
	})

	t.Run("anonymous strips a spoofed header and sets none", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://backend/", nil)
		req.Header.Set("X-Skywire-Remote-PK", "forged") // must not survive

		stampSkywireIdentity(req, cipher.PubKey{}, false, "dmsg")

		if got := req.Header.Get("X-Skywire-Remote-PK"); got != "" {
			t.Errorf("PK = %q, want absent for unidentified peer", got)
		}
		if got := req.Header.Get("X-Skywire-Transport"); got != "dmsg" {
			t.Errorf("transport = %q, want dmsg", got)
		}
	})
}

// TestBuildReverseProxy_HostRewriteDefault checks the historical
// default — preserveHost=false → backend sees Host = target.Host —
// because some apps validate Host against their listening address.
func TestBuildReverseProxy_HostRewriteDefault(t *testing.T) {
	var gotHost string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		_, _ = w.Write([]byte("ok")) //nolint:errcheck,gosec
	}))
	defer backend.Close()

	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	rp := buildReverseProxy(logging.MustGetLogger("test"), target, false)
	front := httptest.NewServer(rp)
	defer front.Close()

	req, _ := http.NewRequest("GET", front.URL+"/x", nil) //nolint:errcheck,gosec
	req.Host = "example.com.0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c.skynet"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()          //nolint:errcheck,gosec
	body, _ := io.ReadAll(resp.Body) //nolint:errcheck,gosec
	if string(body) != "ok" {
		t.Fatalf("body=%q", body)
	}

	// preserveHost=false → Host on the backend should be target.Host
	// (the httptest backend's address), NOT the visitor's Host.
	if gotHost == "" {
		t.Fatalf("backend got empty Host")
	}
	if !strings.HasPrefix(gotHost, "127.0.0.1:") {
		t.Errorf("preserveHost=false: backend Host=%q, expected target's 127.0.0.1:<port>", gotHost)
	}
	if strings.Contains(gotHost, "example.com") {
		t.Errorf("preserveHost=false: backend Host=%q leaked visitor Host", gotHost)
	}
}

// TestBuildReverseProxy_HostPreserved is the new branch. With
// preserveHost=true the backend sees whatever Host the incoming
// request carried — Caddy/nginx vhost dispatch path.
func TestBuildReverseProxy_HostPreserved(t *testing.T) {
	var gotHost string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		_, _ = w.Write([]byte("ok")) //nolint:errcheck,gosec
	}))
	defer backend.Close()

	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	rp := buildReverseProxy(logging.MustGetLogger("test"), target, true)
	front := httptest.NewServer(rp)
	defer front.Close()

	const visitorHost = "magnetosphere.net"
	req, _ := http.NewRequest("GET", front.URL+"/x", nil) //nolint:errcheck,gosec
	req.Host = visitorHost

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()          //nolint:errcheck,gosec
	body, _ := io.ReadAll(resp.Body) //nolint:errcheck,gosec
	if string(body) != "ok" {
		t.Fatalf("body=%q", body)
	}

	if gotHost != visitorHost {
		t.Errorf("preserveHost=true: backend Host=%q, want %q", gotHost, visitorHost)
	}
}
