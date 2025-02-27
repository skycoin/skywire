// Package commands cmd/conf/commands/root.go
package commands

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire"
)

func init() {
	RootCmd.AddCommand(
		ServicesConfCmd,
		DmsghttpConfCmd,
	)
}

// RootCmd is the root command
var RootCmd = &cobra.Command{
	Use:                   "conf",
	Short:                 `print json config files`,
	Long:                  `print json config files`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}

// ServicesConfCmd is the command to print the services-config.json
var ServicesConfCmd = &cobra.Command{
	Use:                   "svcconf",
	Short:                 `print services-config.json file`,
	Long:                  `print services-config.json file`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("%s\n", string(skywire.ServicesJSON))
	},
}

// DmsghttpConfCmd is the command to print the dmsghttp-config.json
var DmsghttpConfCmd = &cobra.Command{
	Use:                   "dmsgconf",
	Short:                 `print dmsghttp-config.json file`,
	Long:                  `print dmsghttp-config.json file`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("%s\n", string(skywire.DmsghttpJSON))
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
