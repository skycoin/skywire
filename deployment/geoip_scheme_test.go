// Package deployment deployment/geoip_scheme_test.go c1-net-deployment
package deployment

import (
	"strings"
	"testing"
)

// TestGeoIPIsHTTPS: the geoip URL is fetched by the BROWSER (the wasm visor and
// the manager UI look up each dmsg server's location), so a plaintext value is
// a subresource the page cannot load once it is served over HTTPS.
//
// It was "http://ip.skycoin.com", which worked on the :8000 HTTP harness and
// was blocked on the :8443 HTTPS one — every per-server lookup rejected as
// "Mixed Content: ... requested an insecure resource", nine at a time, one per
// dmsg server. https serves the identical JSON on both hosts, and an https
// subresource is fine on an http page, so https is correct in both directions.
func TestGeoIPIsHTTPS(t *testing.T) {
	for name, d := range map[string]Services{"Prod": Prod, "Test": Test} {
		if d.GeoIP == "" {
			continue
		}
		if !strings.HasPrefix(d.GeoIP, "https://") {
			t.Errorf("%s.GeoIP = %q; must be https:// — the browser fetches it from an HTTPS page", name, d.GeoIP)
		}
	}
}
