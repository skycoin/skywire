// Package dmsgsrv pkg/services/dmsgsrv/entry_version_test.go c1-net-dmsg
package dmsgsrv

import (
	"testing"

	"github.com/skycoin/skywire/pkg/buildinfo"
)

// The discovery entry must advertise the same build string /health reports and
// every visor shows — release tag plus commit. Advertising the bare tag made
// all nine production servers identical in discovery, so there was no way to
// tell which commit any of them ran, and no way to confirm a shipped
// dmsg-server fix had reached them.
func TestDmsgEntryVersionCarriesTheCommit(t *testing.T) {
	got := dmsgEntryVersion()
	want := buildinfo.Get().Version

	if got == "" {
		// A dev build with no version info advertises nothing, so the entity
		// keeps its "0.0.1" default. Nothing further to assert.
		if want != "" && want != "unknown" {
			t.Errorf("dmsgEntryVersion() = %q, want %q", got, want)
		}
		return
	}
	if got != want {
		t.Errorf("dmsgEntryVersion() = %q, want %q — the entry must match what /health reports", got, want)
	}
	// The whole point: it must not stop at the bare release tag when a commit
	// is available to distinguish builds within that release.
	if c := buildinfo.Commit(); c != "" && c != "unknown" && got == buildinfo.Version() && want != buildinfo.Version() {
		t.Errorf("dmsgEntryVersion() = %q is the bare version; the commit %q is available and must be included", got, c)
	}
}
