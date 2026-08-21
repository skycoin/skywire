// Package ping cmd/skywire-cli/commands/visor/ping/root.go c4-vis-cli
package ping

import (
	"github.com/spf13/cobra"
)

// RootCmd is the ping command that subcommands attach to
// Subcommands register themselves in their own init() functions
// When called with a PK argument directly, it delegates to pingCmd
var RootCmd = &cobra.Command{
	Use:   "ping [pk]",
	Short: "Ping commands for testing visor connectivity",
	Long: `Ping commands for testing visor connectivity.

When called with a public key argument, pings that visor directly.

Available subcommands:
  ping <pk>       - Ping a specific visor (route or --dmsg)
  ping test       - Test connectivity to public visors
  ping bandwidth  - Sustained throughput test to a visor
  ping mux-bw     - Multiplexed-route bandwidth + queueing-delay probe
  ping mux-bw-tui - Interactive TUI for the mux bandwidth probe
  ping tree       - Ping-tree over the route graph (scrollable TUI)
  ping tree-stream- Ping-tree as streamed rows / NDJSON
  ping stop-all   - Stop all active ping connections`,
}
