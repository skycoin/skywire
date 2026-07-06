// Package commands cmd/apps/vpn-client/commands/vpn-client_test.go
//
// Covers the package's one pure, self-contained helper, envInt. The rest of the
// package (RunVPNClient and the set* helpers) is the app runtime: it calls
// app.NewClient, which Fatals without the visor-injected environment, and then
// runs the live VPN client loop — none of which is reachable in a unit test
// without refactoring, which is intentionally out of scope here.
package commands

import (
	"testing"
)

// TestEnvInt covers envInt's parsing rules: unset, valid, zero, negative and
// non-numeric values.
func TestEnvInt(t *testing.T) {
	const key = "VPN_CLIENT_ENVINT_TEST"

	cases := []struct {
		name string
		set  bool
		val  string
		want int
	}{
		{name: "unset", set: false, want: 0},
		{name: "empty", set: true, val: "", want: 0},
		{name: "valid positive", set: true, val: "7", want: 7},
		{name: "zero", set: true, val: "0", want: 0},
		{name: "negative is rejected", set: true, val: "-3", want: 0},
		{name: "non-numeric is rejected", set: true, val: "abc", want: 0},
		{name: "trailing junk is rejected", set: true, val: "12x", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.val)
			} else {
				// t.Setenv requires a value; emulate "unset" by querying a key
				// that is never set in this process.
				if got := envInt("VPN_CLIENT_ENVINT_NEVER_SET"); got != 0 {
					t.Fatalf("expected 0 for unset key, got %d", got)
				}
				return
			}
			if got := envInt(key); got != tc.want {
				t.Fatalf("envInt(%q=%q) = %d, want %d", key, tc.val, got, tc.want)
			}
		})
	}
}
