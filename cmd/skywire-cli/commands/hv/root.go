// Package clihv cmd/skywire-cli/commands/hv/root.go c4-vis-cli
package clihv

import (
	"github.com/spf13/cobra"
)

// RootCmd is the `hv` command group: tools for building, serving, driving and
// bridging the hypervisor UI / wasm-visor.
var RootCmd = &cobra.Command{
	Use:   "hv",
	Short: "Hypervisor / wasm-visor tools",
	Long: `Tools for building, serving, driving and bridging the hypervisor UI and the
standalone wasm-visor.

Build & serve:
  gen      generate a self-contained standalone hypervisor.html (opens from file://)
  serve    serve the keyless standalone wasm-visor over HTTP (reverse-proxy with Caddy)

Desktop bridge:
  notify   show a remote visor's app notifications on THIS machine (SSE bridge)

Browser automation (CDP — needs --remote-debugging-port=9222):
  probe    watch one page load and stream console, exceptions and crashes
  shell    drive the visor shell in a browser tab and capture the result
  eval     evaluate JavaScript on a specific CDP target by webSocketDebuggerUrl`,
}

func init() {
	RootCmd.AddCommand(genCmd)
}
