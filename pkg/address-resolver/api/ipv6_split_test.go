// Package api — pkg/address-resolver/api/ipv6_split_test.go
//
// Verifies splitFamilyAddr family detection over the input shapes
// the AR bind handlers actually feed it: bare host (STCPR style),
// host:port (SUDPH style), and edge cases (empty, unparseable,
// IPv4-mapped IPv6). Paired with the per-family merge in
// store.Bind, this is the round-trip that lets a dual-stack visor
// register both families in one AR record. See #1525 Phase 1.
package api

import (
	"testing"
)

func TestSplitFamilyAddr(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		wantV4 string
		wantV6 string
	}{
		// STCPR-style bare host inputs.
		{"v4 bare host", "203.0.113.5", "203.0.113.5", ""},
		{"v6 bare host", "2001:db8::1", "", "2001:db8::1"},
		{"v6 loopback", "::1", "", "::1"},

		// SUDPH-style host:port inputs.
		{"v4 host:port", "203.0.113.5:5060", "203.0.113.5:5060", ""},
		{"v6 host:port bracketed", "[2001:db8::1]:5060", "", "[2001:db8::1]:5060"},

		// Backward-compat fallbacks: unparseable or empty inputs.
		// Historically these would have gone into the single
		// RemoteAddr field unchanged; preserve that contract so we
		// don't regress on a hostname-style PublicIP declaration.
		{"non-IP hostname", "visor.example.com", "visor.example.com", ""},
		{"empty", "", "", ""},

		// IPv4-mapped IPv6 (::ffff:1.2.3.4) is semantically v4
		// traffic — must classify as v4 so dialers don't try a
		// useless v6 route. net.ParseIP().To4() does this for us.
		{"v4-mapped v6", "::ffff:203.0.113.5", "::ffff:203.0.113.5", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v4, v6 := splitFamilyAddr(tc.in)
			if v4 != tc.wantV4 || v6 != tc.wantV6 {
				t.Errorf("splitFamilyAddr(%q) = (%q, %q); want (%q, %q)",
					tc.in, v4, v6, tc.wantV4, tc.wantV6)
			}
		})
	}
}
