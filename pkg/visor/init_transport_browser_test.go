// Package visor pkg/visor/init_transport_browser_test.go c3-vis-core
package visor

import (
	"os"
	"regexp"
	"testing"
)

// TestBrowserSkipsSocketCarriers. A browser has neither TCP nor UDP sockets, so
// stcpr, sudph, quic and the STUN probe cannot work there. Starting one anyway
// is not merely useless: the client registers, IsKnownNetwork reports the
// carrier usable, and autoconnect spends a whole phase dialing it — every
// attempt waiting out its full timeout. That shipped for squicr and for STUN,
// which both missed the guard stcpr and sudph already had.
//
// This reads the source rather than calling the functions because the guard is
// a runtime.GOOS check: on the test host GOOS is never "js", so no amount of
// calling them exercises the branch. What can be checked, and is what regressed,
// is that each of these carrier inits still HAS the guard.
func TestBrowserSkipsSocketCarriers(t *testing.T) {
	src, err := os.ReadFile("init_transport.go")
	if err != nil {
		t.Fatalf("read init_transport.go: %v", err)
	}
	for _, fn := range []string{
		"initStcprClient",
		"initSudphClient",
		"initQuicClient",
		"initStunClient",
	} {
		// The guard must be the FIRST thing the function does: past any dial or
		// bind it has already cost what it was meant to save.
		re := regexp.MustCompile(`(?s)func ` + fn + `\([^)]*\) error \{\s*if runtime\.GOOS == "js" \{`)
		if !re.Match(src) {
			t.Errorf("%s does not open with a runtime.GOOS == \"js\" guard; "+
				"a browser has no socket for it and autoconnect will dial it anyway", fn)
		}
	}
}
