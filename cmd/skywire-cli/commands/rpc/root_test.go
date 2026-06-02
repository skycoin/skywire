// Package clirpc root_test.go
package clirpc

import "testing"

// TestIsUnderBase locks in the boundary behavior that distinguishes
// base+suffix from sub-paths beneath it. The pre-fix prefix match
// caused /all-transports/per-key-stats to alias onto /all-transports
// and poisoned the CXO subscriber-cache for the 2026-05-31 reward day.
func TestIsUnderBase(t *testing.T) {
	const (
		base   = "https://tpd.skywire.skycoin.com"
		suffix = "/all-transports"
	)
	cases := []struct {
		url  string
		want bool
	}{
		{base + suffix, true},                          // exact
		{base + suffix + "?selfTransports=true", true}, // query
		{base + suffix + "#frag", true},                // fragment
		{base + suffix + "/per-key-stats", false},      // sub-path — the bug
		{base + suffix + "/stats", false},              // sub-path — sibling
		{base + suffix + "extra", false},               // no boundary char
		{base + "/other", false},                       // unrelated path
		{"", false},                                    // empty
	}
	for _, c := range cases {
		if got := isUnderBase(c.url, base, suffix); got != c.want {
			t.Errorf("isUnderBase(%q,%q,%q) = %v, want %v", c.url, base, suffix, got, c.want)
		}
	}
	if isUnderBase("https://x/y", "", "/y") {
		t.Error("empty base must never match")
	}
}
