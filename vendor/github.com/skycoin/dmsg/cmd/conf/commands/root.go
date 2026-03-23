// Package commands cmd/conf/commands/root.go
package commands

import (
	"log"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgclient"
)

// RootCmd is the root command
var RootCmd = &cobra.Command{
	Short:                 `dmsg deployment servers config`,
	Long:                  `print the dmsg servers from the dmsghttp-config`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		_, err := script.Echo(string(dmsg.DmsghttpJSON)).JQ(`.prod.dmsg_servers`).Stdout()
		if err != nil {
			log.Fatal("Failed to execute command: ", err)
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}
