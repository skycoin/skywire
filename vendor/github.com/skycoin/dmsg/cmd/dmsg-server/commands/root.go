// Package commands cmd/dmsg-server/commands/root.go
package commands

import (
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/cmd/dmsg-server/commands/config"
	"github.com/skycoin/dmsg/cmd/dmsg-server/commands/start"
	"github.com/skycoin/dmsg/pkg/dmsgclient"
)

func init() {
	RootCmd.AddCommand(
		config.RootCmd,
		start.RootCmd,
	)

}

// RootCmd contains the root dmsg-server command
var RootCmd = &cobra.Command{
	Use:   dmsgclient.ExecName(),
	Short: "DMSG Server",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐   ┌─┐┌─┐┬─┐┬  ┬┌─┐┬─┐
	││││││└─┐│ ┬ ─ └─┐├┤ ├┬┘└┐┌┘├┤ ├┬┘
	─┴┘┴ ┴└─┘└─┘   └─┘└─┘┴└─ └┘ └─┘┴└─
DMSG Server - relays DMSG traffic between clients.

HTTP Endpoints:
  GET  /health     Health check

Example:
  skywire dmsg server config gen -o dmsg-config.json
  skywire dmsg server start dmsg-config.json`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
}

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}
