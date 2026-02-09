// Package commands implements the skywire release commands with hardware wallet support.
//
// Note: On 386 architecture, the hardware wallet subcommand is a stub due to
// limitations in the github.com/google/gousb library.
package commands

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"

	skyhw "github.com/skycoin/skycoin/cmd/hardware-wallet/commands"
	cli "github.com/skycoin/skywire/cmd/skywire-cli/commands"
	visor "github.com/skycoin/skywire/pkg/visor"
)

var (
	bv bool
	di bool
)

func init() {
	RootCmd.AddCommand(
		visor.RootCmd,
		cli.RootCmd,
		skyhw.RootCmd,
	)
	visor.RootCmd.Use = "visor"
	visor.RootCmd.Short = "skywire visor"
	cli.RootCmd.Use = "cli"
	cli.RootCmd.Short = "skywire command line interface"
	skyhw.RootCmd.Use = "skyhw"
	skyhw.RootCmd.Short = "skycoin hardware wallet utilities"

	if fmt.Sprintf("%v", buildinfo.DebugBuildInfo()) != "" {
		RootCmd.Flags().BoolVarP(&di, "info", "d", false, "print runtime/debug.BuildInfo")
	}
	if fmt.Sprintf("%v", buildinfo.DBIVersion()) != "" {
		RootCmd.Flags().BoolVarP(&bv, "bv", "b", false, "print runtime/debug.BuildInfo.Main.Version")
	}
}

// RootCmd contains visor, cli, and hardware wallet utilities
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Long: func() (ret string) {
		ret = `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
	└─┐├┴┐└┬┘││││├┬┘├┤
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘`
		if buildinfo.DBIVersion() != "" {
			ret += fmt.Sprintf("\n%v", buildinfo.DBIVersion())
		} else {
			ret += fmt.Sprintf("\nskywire version %v", buildinfo.Version())
		}
		if buildinfo.Go() != "unknown" && buildinfo.Go() != "" {
			ret += "\nbuilt with " + buildinfo.Go()
		}
		return ret
	}(),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(cmd *cobra.Command, _ []string) {
		if di {
			fmt.Printf("%v\n", buildinfo.DebugBuildInfo())
			return
		}
		if bv {
			fmt.Printf("%v\n", buildinfo.DBIVersion())
			return
		}
		if err := cmd.Help(); err != nil {
			log.Printf("Failed to print help: %v", err)
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
