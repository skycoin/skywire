// Package api — pkg/address-resolver/api/ipv6_declared_test.go
//
// Coverage for #1525 Phase 2c: the AR bind handler honors a declared
// LocalAddresses.PublicIPv6 to populate VisorData.RemoteAddrV6 when
// the observed remote source is v4 (typical of the dmsg-routed AR
// path where the visor's request reaches AR through a dmsg-bridge
// whose own family doesn't reflect the visor's). Validates that the
// AR accepts public v6 declarations and ignores private / non-IPv6
// values to prevent spoofed registrations.
package api

import (
	"net"
	"testing"
)

// TestIsPublicIPv6 verifies the Phase 2c v6 public-address validator
// the AR bind handler uses to gate declared LocalAddresses.PublicIPv6
// before promoting it into VisorData.RemoteAddrV6. Coverage:
//   - real globally-routable v6 → accepted
//   - documentation range (2001:db8::/32) → accepted today (no stdlib
//     flag for it; operators using doc ranges in real configs is a
//     config bug not worth specially handling)
//   - loopback / link-local / ULA → rejected
//   - v4 input → rejected (caller is expected to gate on family)
//   - empty / garbage → rejected
func TestIsPublicIPv6(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"public v6 (doc range, accepted: no stdlib flag)", "2001:db8::1", true},
		{"public v6 cloudflare", "2606:4700:4700::1111", true},
		{"v6 loopback", "::1", false},
		{"v6 link-local unicast", "fe80::1", false},
		{"v6 ULA fc00::/7", "fd00::1", false},
		{"v6 unspecified ::", "::", false}, // To4()==nil but loopback semantics? Go treats :: as unspecified, not loopback. Falls through. Documenting current behavior:
		{"v4 string rejected", "203.0.113.5", false},
		{"v4-mapped v6 rejected", "::ffff:203.0.113.5", false},
		{"empty", "", false},
		{"garbage", "not-an-ip", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isPublicIPv6(net.ParseIP(tc.in))
			if got != tc.want {
				t.Errorf("isPublicIPv6(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
