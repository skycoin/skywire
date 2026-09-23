//go:build !mobile

// Package visor pkg/visor/fetchdest_test.go c3-vis-core
package visor

import (
	"net/http"
	"testing"
)

// TestFetchDest covers the signal the dashboard-vs-desk choice turns on.
//
// Sec-Fetch-Dest is a forbidden header name, so it never survives a service
// worker: bottle's vnet worker cannot read it off the incoming request and so
// cannot put it on the outgoing one. Every request reaching us over the vnet
// wire therefore looks unframed, and the hypervisor UI reloaded into the desk
// instead of the dashboard. The worker forwards request.destination as
// X-Vnet-Fetch-Dest instead; both must be honored.
func TestFetchDest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"framed over the network", map[string]string{"Sec-Fetch-Dest": "iframe"}, "iframe"},
		{"framed through the vnet worker", map[string]string{"X-Vnet-Fetch-Dest": "iframe"}, "iframe"},
		{"top-level over the network", map[string]string{"Sec-Fetch-Dest": "document"}, "document"},
		{"top-level through the vnet worker", map[string]string{"X-Vnet-Fetch-Dest": "document"}, "document"},
		{"nothing said", nil, ""},
		// The browser's own header wins: it is authoritative when the request
		// actually reached us over the network, and a forwarded value is only
		// ever a stand-in for it.
		{"real header beats the forwarded one", map[string]string{
			"Sec-Fetch-Dest":    "document",
			"X-Vnet-Fetch-Dest": "iframe",
		}, "document"},
		// An empty Sec-Fetch-Dest must not mask a forwarded value, or the
		// fallback would never fire on an engine that sends the header blank.
		{"empty real header falls through", map[string]string{
			"Sec-Fetch-Dest":    "",
			"X-Vnet-Fetch-Dest": "iframe",
		}, "iframe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := http.NewRequest(http.MethodGet, "http://visor/", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if got := fetchDest(r); got != tc.want {
				t.Errorf("fetchDest = %q, want %q", got, tc.want)
			}
		})
	}
}
