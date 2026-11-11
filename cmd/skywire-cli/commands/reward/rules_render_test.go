//go:build !tinygo

package clireward

import (
	"regexp"
	"testing"

	"github.com/skycoin/skywire/rewards"
)

// Every #anchor the rules link to, and the ones the rewards site links to
// from its ineligible table, must be a heading id in the rendered rules.
func TestRulesAnchorsResolve(t *testing.T) {
	out, err := rulesHTML(rewards.MainnetRules)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`<h[1-6] id="([^"]+)"`).FindAllStringSubmatch(out, -1) {
		ids[m[1]] = true
	}
	want := []string{"1-version", "transports", "per-machine-limit", "survey"}
	for _, m := range regexp.MustCompile(`\]\(#([^)]+)\)`).FindAllStringSubmatch(rewards.MainnetRules, -1) {
		want = append(want, m[1])
	}
	for _, a := range want {
		if !ids[a] {
			t.Errorf("no heading with id %q", a)
		}
	}
}
