// Package clihv cmd/skywire-cli/commands/hv/root.go c4-vis-cli
package clihv

import (
	"github.com/spf13/cobra"
)

// RootCmd is the `hv` command group: tools for serving, driving and
// bridging the hypervisor UI / wasm-visor.
var RootCmd = &cobra.Command{
	Use:   "hv",
	Short: "Hypervisor / wasm-visor tools",
	Long: `Tools for serving, driving and bridging the hypervisor UI and the
standalone wasm-visor.

Serve:
  serve    serve the keyless standalone wasm-visor desk over HTTP (reverse-proxy with Caddy)

Desktop bridge:
  notify   show a remote visor's app notifications on THIS machine (SSE bridge)

Browser automation (needs --remote-debugging-port):
  probe    watch one page load and stream console, exceptions and crashes
  eval     evaluate JavaScript in a page
  drive    hold a persistent WebDriver BiDi session on a local control port
  shell    drive the visor shell in a browser tab and capture the result

probe and eval speak CDP to Chromium/Brave and WebDriver BiDi to
Waterfox/Firefox, detected from the debug port. shell is CDP-only: it
synthesizes keystrokes, which BiDi's input.* module would be needed for.

Firefox allows ONE BiDi session and does not release it when a socket drops.
Run drive once and point --driver at it rather than spending a session per
command.`,
}
