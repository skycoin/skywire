//go:build js && wasm

// Package clivisorping cmd/skywire-cli/commands/visor/ping/tui_js.go c5-cli-visor
package ping

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func noTUIJS(name string) {
	fmt.Fprintf(os.Stderr, "%s: interactive TUI is not available in the browser build\n", name)
	os.Exit(1)
}

// runPingTree: stub — no interactive terminal in the browser build.
func runPingTree(_ *cobra.Command, _ []string) { noTUIJS("ping tree") }

// runMuxBandwidthTUI: stub — no interactive terminal in the browser build.
func runMuxBandwidthTUI(_ *cobra.Command, _ []string) { noTUIJS("ping mux-bw-tui") }
