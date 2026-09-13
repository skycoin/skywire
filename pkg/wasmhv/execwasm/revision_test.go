// Package execwasm pkg/wasmhv/execwasm/revision_test.go c3-wasm-embed
package execwasm

import "testing"

// TestRevision_MatchesPresence: Revision is only meaningful alongside a staged
// module. A source build stages neither, and must report "" rather than a
// leftover — otherwise the staleness warning compares against a revision for a
// module that is not there.
func TestRevision_MatchesPresence(t *testing.T) {
	if !Present() {
		if got := Revision(); got != "" {
			t.Errorf("Revision() = %q with no module staged; want \"\"", got)
		}
		return
	}
	// With a module staged the revision is optional (it may predate the
	// Makefile writing it), but when set it must be a plain commit hash on one
	// line — the warning prints it, and Makefile check-exec-wasm compares it to
	// `git rev-parse HEAD` verbatim.
	rev := Revision()
	if rev == "" {
		t.Skip("module staged without a recorded revision (stage it again with make embed-exec-wasm)")
	}
	if len(rev) != 40 {
		t.Errorf("Revision() = %q (%d chars); want a 40-char commit hash", rev, len(rev))
	}
	for _, c := range rev {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("Revision() = %q; want lowercase hex only", rev)
		}
	}
}
