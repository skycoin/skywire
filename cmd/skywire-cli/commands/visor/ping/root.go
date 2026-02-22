// Package ping cmd/skywire-cli/commands/visor/ping/root.go
package ping

import (
	"github.com/spf13/cobra"
)

// RootCmd is the ping command that subcommands attach to
// Subcommands register themselves in their own init() functions
var RootCmd = &cobra.Command{
	Use:   "ping",
	Short: "Ping commands for testing visor connectivity",
	Long: `Ping commands for testing visor connectivity.

Available subcommands:
  ping <pk>     - Ping a specific visor
  ping test     - Test connectivity to public visors
  ping tree     - Ping visors via transport routes (tree view)
  ping tree2    - Ping visors via transport routes (scrollable TUI)
  ping graph    - Ping visors reachable from this visor
  ping stop-all - Stop all active ping connections`,
}
