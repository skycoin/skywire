// Package visor pkg/visor/meshproxy_test.go c4-vis-mesh
package visor

import (
	"net/http"
	"testing"
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
