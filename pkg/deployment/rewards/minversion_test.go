// Package rewards pkg/deployment/rewards/minversion_test.go c3-app-rewards
package rewards

import (
	"testing"
	"time"
)

// TestMinVersion checks the minimum against the real release dates around
// 2026-11-25: the highest release out 14 days, skipping mobile tags and
// prereleases.
func TestMinVersion(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	releases := []Release{
		{Tag: "develop-latest", Prerelease: true, Published: at("2026-08-26T17:48:41Z")},
		{Tag: "v1.3.113", Published: at("2026-11-25T00:29:05Z")},
		{Tag: "mobile-v0.0.13", Published: at("2026-11-20T12:27:42Z")},
		{Tag: "v1.3.112", Published: at("2026-11-16T11:43:45Z")},
		{Tag: "v1.3.110", Published: at("2026-11-11T21:56:54Z")},
		{Tag: "v1.3.109", Published: at("2026-11-09T21:49:39Z")},
		{Tag: "v1.3.107", Published: at("2026-11-07T21:45:51Z")},
		{Tag: "v1.3.200", Draft: true, Published: at("2026-10-01T00:00:00Z")},
	}
	for when, want := range map[string]string{
		"2026-11-26T00:00:00Z": "v1.3.110",
		"2026-11-25T00:00:00Z": "v1.3.109",
		"2026-11-25T22:00:00Z": "v1.3.110",
		"2026-11-25T21:00:00Z": "v1.3.109",
		"2026-11-30T12:00:00Z": "v1.3.112",
		"2026-11-01T00:00:00Z": "",
	} {
		if got := MinVersion(releases, at(when)); got != want {
			t.Errorf("at %s: %q, want %q", when, got, want)
		}
	}
}

func TestVersionBelow(t *testing.T) {
	for _, c := range []struct {
		v     string
		below bool
	}{
		{"v1.3.107", true},
		{"v1.3.110", false},
		{"v1.3.113-26-g327848978-327848978903", false},
		{"v1.3.109+dirty", true},
		{"", true},
		{"latest", true},
	} {
		if got := VersionBelow(c.v, "v1.3.110"); got != c.below {
			t.Errorf("VersionBelow(%q) = %v, want %v", c.v, got, c.below)
		}
	}
}
