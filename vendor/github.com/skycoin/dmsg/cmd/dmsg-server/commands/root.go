// Package commands cmd/dmsg-server/commands/root.go
package commands

import (
	"log"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/cmd/dmsg-server/commands/config"
	"github.com/skycoin/dmsg/cmd/dmsg-server/commands/start"
)

func init() {
	RootCmd.AddCommand(
		config.RootCmd,
		start.RootCmd,
	)

}

// RootCmd contains the root dmsg-server command
var RootCmd = &cobra.Command{
	Use:   "server",
	Short: "DMSG Server",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐   ┌─┐┌─┐┬─┐┬  ┬┌─┐┬─┐
	││││││└─┐│ ┬ ─ └─┐├┤ ├┬┘└┐┌┘├┤ ├┬┘
	─┴┘┴ ┴└─┘└─┘   └─┘└─┘┴└─ └┘ └─┘┴└─
  ` + "DMSG Server",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
