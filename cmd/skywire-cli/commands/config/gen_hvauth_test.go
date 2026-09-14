// Package cliconfig cmd/skywire-cli/commands/config/gen_hvauth_test.go c4-vis-cli
package cliconfig

import "testing"

// hypervisor.enable_auth had no skywire.conf variable, so on a host where
// the config is rebuilt from scratch (no prior config to retain from) the
// gate always came back on, and an operator's choice could not be made
// declarative. HVAUTH fills that gap; the parse must refuse anything it
// does not recognize rather than guess, because guessing wrong either
// exposes an unauthenticated UI or locks the operator out.
func TestHvAuthFromEnv(t *testing.T) {
	for _, tc := range []struct {
		in        string
		want, tok bool
	}{
		{"true", true, true},
		{"TRUE", true, true},
		{"  true  ", true, true},
		{"1", true, true},
		{"yes", true, true},
		{"false", false, true},
		{"False", false, true},
		{"0", false, true},
		{"no", false, true},
		{"", false, false},
		{"   ", false, false},
		{"maybe", false, false},
		{"tru", false, false},
		{"2", false, false},
	} {
		got, ok := hvAuthFromEnv(tc.in)
		if ok != tc.tok {
			t.Errorf("hvAuthFromEnv(%q): ok = %v, want %v", tc.in, ok, tc.tok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("hvAuthFromEnv(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
