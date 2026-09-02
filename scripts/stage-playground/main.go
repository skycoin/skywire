// Command stage-playground writes the playground's embedded page assets —
// bundle.js (the browseui desk bundle) and winbox.wasm (the window manager,
// inflated) — into the directory given as the first argument. The wasm
// modules themselves (skywire.wasm.gz, wasm-visor.wasm.gz, wasm_exec.js) are
// staged by the `make playground` target that runs this; together they make
// build/playground a fully static page for the docs site.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/skycoin/skywire/pkg/wasmhv/browseui"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: stage-playground <outdir>")
		os.Exit(2)
	}
	out := os.Args[1]
	if err := os.MkdirAll(out, 0o750); err != nil {
		fmt.Fprintln(os.Stderr, "stage-playground:", err)
		os.Exit(1)
	}
	files := map[string][]byte{
		"bundle.js":   browseui.BrowseJS,
		"winbox.wasm": browseui.WinBoxWasm(),
	}
	for name, data := range files {
		p := filepath.Join(out, name)
		if err := os.WriteFile(p, data, 0o644); err != nil { //nolint:gosec
			fmt.Fprintln(os.Stderr, "stage-playground:", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s (%d bytes)\n", p, len(data))
	}
}
