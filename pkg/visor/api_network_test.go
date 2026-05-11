package visor

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/logging"
)

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
