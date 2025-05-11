// Package commands cmd/skywire-services/commands/services.go
package commands

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/spf13/cobra"

	ar "github.com/skycoin/skywire/cmd/address-resolver/commands"
	confbs "github.com/skycoin/skywire/cmd/config-bootstrapper/commands"
	nm "github.com/skycoin/skywire/cmd/network-monitor/commands"
	rf "github.com/skycoin/skywire/cmd/route-finder/commands"
	sd "github.com/skycoin/skywire/cmd/service-discovery/commands"
	sn "github.com/skycoin/skywire/cmd/setup-node/commands"
	se "github.com/skycoin/skywire/cmd/sw-env/commands"
	tpd "github.com/skycoin/skywire/cmd/transport-discovery/commands"
	tps "github.com/skycoin/skywire/cmd/transport-setup/commands"
	ut "github.com/skycoin/skywire/cmd/uptime-tracker/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
)

func init() {
	RootCmd.AddCommand(
		tpd.RootCmd,
		tps.RootCmd,
		ar.RootCmd,
		rf.RootCmd,
		confbs.RootCmd,
		se.RootCmd,
		ut.RootCmd,
		sd.RootCmd,
		sn.RootCmd,
		nm.RootCmd,
	)
	tpd.RootCmd.Use = "tpd"
	tps.RootCmd.Use = "tps"
	ar.RootCmd.Use = "ar"
	rf.RootCmd.Use = "rf"
	confbs.RootCmd.Use = "confbs"
	se.RootCmd.Use = "se"
	ut.RootCmd.Use = "ut"
	sd.RootCmd.Use = "sd"
	sn.RootCmd.Use = "sn"
	nm.RootCmd.Use = "nm"
}

// RootCmd contains all subcommands
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "Skywire services",
	Long: calvin.AsciiFont("skywire-cli") + `
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
