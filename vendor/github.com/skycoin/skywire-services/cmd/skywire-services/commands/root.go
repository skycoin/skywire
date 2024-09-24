// Package commands cmd/skywire-services/commands/services.go
package commands

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/spf13/cobra"

	ar "github.com/skycoin/skywire-services/cmd/address-resolver/commands"
	confbs "github.com/skycoin/skywire-services/cmd/config-bootstrapper/commands"
	kg "github.com/skycoin/skywire-services/cmd/keys-gen/commands"
	nv "github.com/skycoin/skywire-services/cmd/node-visualizer/commands"
	rf "github.com/skycoin/skywire-services/cmd/route-finder/commands"
	se "github.com/skycoin/skywire-services/cmd/sw-env/commands"
	tpd "github.com/skycoin/skywire-services/cmd/transport-discovery/commands"
	tps "github.com/skycoin/skywire-services/cmd/transport-setup/commands"
	ut "github.com/skycoin/skywire-services/cmd/uptime-tracker/commands"
)

func init() {
	RootCmd.AddCommand(
		tpd.RootCmd,
		tps.RootCmd,
		ar.RootCmd,
		rf.RootCmd,
		confbs.RootCmd,
		kg.RootCmd,
		nv.RootCmd,
		se.RootCmd,
		ut.RootCmd,
	)
	tpd.RootCmd.Use = "tpd"
	tps.RootCmd.Use = "tps"
	ar.RootCmd.Use = "ar"
	rf.RootCmd.Use = "rf"
	confbs.RootCmd.Use = "confbs"
	kg.RootCmd.Use = "kg"
	nv.RootCmd.Use = "nv"
	se.RootCmd.Use = "se"
	ut.RootCmd.Use = "ut"
}

// RootCmd contains all subcommands
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Skywire services",
	Long: `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐┌─┐
	└─┐├┴┐└┬┘││││├┬┘├┤───└─┐├┤ ├┬┘└┐┌┘││  ├┤ └─┐
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  └─┘└─┘┴└─ └┘ ┴└─┘└─┘└─┘
	Skywire services`,
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
