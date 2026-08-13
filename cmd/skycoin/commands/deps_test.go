package commands

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNoPackageLinksSkycoinsCipherWasm pins the reason this package exists.
//
// skywire assembles the `skywire skycoin` tree itself rather than importing
// skycoin's cmd/skycoin-wallet/commands, because that assembly imports
// cmd/skycoin-web/wasmassets — where skycoin's binaries pick up the skycoin-lite
// cipher wasm. skywire already embeds the wasm visor, which publishes the same
// Cipher and CipherExtras API, so importing skycoin's assembly means carrying
// the cipher twice.
//
// This asserts over the WHOLE module, not just this package. A per-package
// check is what let cmd/skycoin-skywire — a second combined binary — keep
// importing skycoin's assembly after this one stopped: the tree looked right,
// everything compiled, and `go mod vendor` quietly kept pulling the 1.8 MB blob
// back in. One importer anywhere is enough to put it in the vendor directory and
// in that binary.
//
// Re-adding any of these imports would compile cleanly and produce an identical
// command tree. Only the dependency graph shows it.
func TestNoPackageLinksSkycoinsCipherWasm(t *testing.T) {
	forbidden := map[string]string{
		"github.com/skycoin/skycoin/src/skycoin-lite/wasm-go":     "the std-Go cipher wasm blob",
		"github.com/skycoin/skycoin/src/skycoin-lite/wasm-tinygo": "the TinyGo cipher wasm blob",
		"github.com/skycoin/skycoin/cmd/skycoin-web/wasmassets":   "registers skycoin's own cipher wasm",
		"github.com/skycoin/skycoin/cmd/skycoin-wallet/commands":  "skycoin's assembly, which imports wasmassets",
	}

	out, err := exec.Command("go", "list", "-deps", "github.com/skycoin/skywire/...").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}

	deps := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		deps[strings.TrimSpace(line)] = true
	}

	for pkg, why := range forbidden {
		if deps[pkg] {
			t.Errorf("%s (%s) is in the module's dependency graph; skywire serves the "+
				"wasm visor's cipher instead — see cmd/skycoin/commands/cipherwasm.go", pkg, why)
		}
	}
}

// TestCipherWasmIsRegisteredForWeb guards the opposite regression.
//
// Declining skycoin's cipher means `skywire skycoin web` has none unless this
// package supplies one. If registerCipherWasm stopped being called, or wasmbin
// stopped carrying a blob, the two /assets/scripts routes would 404 and the
// wallet would fail in the browser with no cipher — while everything still
// compiled and every other test passed.
func TestCipherWasmIsRegisteredForWeb(t *testing.T) {
	// init() has already run registerCipherWasm by the time a test executes.
	if !cipherWasmAvailable() {
		t.Error("no cipher wasm registered with skycoin-web; `skywire skycoin web` " +
			"would 404 /assets/scripts/skycoin-lite.wasm and the wallet would have no cipher")
	}
}
