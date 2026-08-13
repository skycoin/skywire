package commands

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDoesNotImportSkycoinWalletAssembly pins the reason this package exists.
//
// skywire mounts skycoin's command packages individually rather than importing
// skycoin's own cmd/skycoin-wallet/commands, because that assembly imports
// cmd/skycoin-web/commands and so links the thin-client wallet's skycoin-lite
// cipher wasm into every skywire binary. Nothing about that is visible at the
// call site: re-adding the one-line import would restore the identical command
// tree, compile cleanly, pass every other test, and silently put ~1.8 MB of
// redundant wasm back. A dependency property is the only thing that catches it.
//
// This does not yet assert the absence of skycoin-lite itself. The `web`
// subcommand is still mounted from cmd/skycoin-web/commands pending a
// skywire-native replacement, so the wasm is still linked; see the package
// comment. When that lands, add skycoin-lite to forbidden below.
func TestDoesNotImportSkycoinWalletAssembly(t *testing.T) {
	forbidden := []string{
		"github.com/skycoin/skycoin/cmd/skycoin-wallet/commands",
	}

	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}

	deps := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		deps[strings.TrimSpace(line)] = true
	}

	for _, pkg := range forbidden {
		if deps[pkg] {
			t.Errorf("%s is in this package's dependency graph; "+
				"skywire assembles skycoin's commands itself precisely to keep it out", pkg)
		}
	}
}
