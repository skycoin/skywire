// Package cligotop cmd/skywire-cli/commands/gotop/label_test.go c4-vis-cli
package cligotop

import (
	"os"
	"strings"
	"testing"
)

// The overlay is read while watching several machines at once, so the hostname
// has to come FIRST — the version is usually identical across them and answers
// a different question. A host with no resolvable name falls back to the bare
// version rather than printing a placeholder that reads like a machine name.
func TestGotopLabelLeadsWithHostname(t *testing.T) {
	got := gotopLabel()
	if got == "" {
		t.Fatal("empty label: the overlay would render blank")
	}
	h, err := os.Hostname()
	if err != nil || h == "" {
		t.Skip("no hostname on this machine — the fallback path is the only one reachable")
	}
	if !strings.HasPrefix(got, h+" ") {
		t.Errorf("label = %q, want it to lead with hostname %q", got, h)
	}
	if strings.TrimSpace(strings.TrimPrefix(got, h)) == "" {
		t.Errorf("label = %q carries no version after the hostname", got)
	}
}
