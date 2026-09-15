// Package doc cmd/skywire/commands/doc/serve_section_test.go c1-cli-doc
package doc

import (
	"testing"

	skydocs "github.com/skycoin/skywire/docs"
)

// TestProseSectionReadsTheDocument. The filename rule was wrong for a quarter
// of the top-level prose, and wrong in both directions — proposals filed as
// reference, and shipped subsystems still filed as proposals. Each case here
// is one the old rule got wrong, so a regression to filename-only fails.
func TestProseSectionReadsTheDocument(t *testing.T) {
	for _, c := range []struct{ file, want string }{
		// Proposals the filename rule called reference, because they carry no
		// -rfc suffix. Each says so in its own opening lines.
		{"mobile-ui-boundary.md", secRFC},
		{"skychat_cxo_tcp_standalone.md", secRFC},
		// Shipped, and filed as proposals by the filename rule. Ten files in
		// pkg/router cite warm_standby_legs_rfc.md as their design reference.
		{"routing_policy_rfc.md", secReference},
		// Removed subsystems. "reference" is where a reader goes for current
		// behavior, which is the worst place for these.
		{"visor-core-convergence.md", secHistory},
		{"wasm-hv-spa-backend.md", secHistory},
		// Genuine drafts, which the filename rule also got right.
		{"skydex-website-over-dmsg-rfc.md", secRFC},
		// No status line at all: the filename is still the fallback.
		{"visor-doctor-rfc.md", secRFC},
		{"glossary.md", secReference},
	} {
		if got := proseSection(skydocs.Prose(), c.file); got != c.want {
			t.Errorf("proseSection(%s) = %q, want %q", c.file, got, c.want)
		}
	}
}
