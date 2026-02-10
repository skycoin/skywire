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

	dmsg "github.com/skycoin/dmsg/cmd/dmsg/commands"
	"github.com/spf13/cobra"

	skyhw "github.com/skycoin/skycoin/cmd/hardware-wallet/commands"
	skycoin "github.com/skycoin/skycoin/cmd/skycoin-wallet/commands"
	sc "github.com/skycoin/skywire/cmd/apps/skychat/commands"
	snc "github.com/skycoin/skywire/cmd/apps/skynet-client/commands"
	sn "github.com/skycoin/skywire/cmd/apps/skynet/commands"
	ssc "github.com/skycoin/skywire/cmd/apps/skysocks-client/commands"
	ss "github.com/skycoin/skywire/cmd/apps/skysocks/commands"
	vpnc "github.com/skycoin/skywire/cmd/apps/vpn-client/commands"
	vpns "github.com/skycoin/skywire/cmd/apps/vpn-server/commands"
	cli "github.com/skycoin/skywire/cmd/skywire-cli/commands"
	services "github.com/skycoin/skywire/cmd/skywire-services/commands"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	visor "github.com/skycoin/skywire/pkg/visor"
)

var (
	bv bool
	di bool
)

func init() {
	// Apps subcommand
	appsCmd.AddCommand(
		vpns.RootCmd,
		vpnc.RootCmd,
		ssc.RootCmd,
		ss.RootCmd,
		sc.RootCmd,
		sn.RootCmd,
		snc.RootCmd,
	)

	// Add hardware wallet to skycoin commands
	skycoin.RootCmd.AddCommand(
		skyhw.RootCmd,
	)

	// Root command
	RootCmd.AddCommand(
		visor.RootCmd,
		cli.RootCmd,
		services.RootCmd,
		dmsg.RootCmd,
		appsCmd,
		skycoin.RootCmd,
	)

	// Set command names
	visor.RootCmd.Use = "visor"
	visor.RootCmd.Short = "skywire visor"
	cli.RootCmd.Use = "cli"
	cli.RootCmd.Short = "skywire command line interface"
	services.RootCmd.Use = "svc"
	services.RootCmd.Short = "skywire services"
	dmsg.RootCmd.Use = "dmsg"
	dmsg.RootCmd.Short = "dmsg services & utilities"
	vpns.RootCmd.Use = "vpn-server"
	vpnc.RootCmd.Use = "vpn-client"
	ssc.RootCmd.Use = "skysocks-client"
	ss.RootCmd.Use = "skysocks"
	sc.RootCmd.Use = "skychat"
	sn.RootCmd.Use = "skynet-srv"
	snc.RootCmd.Use = "skynet-client"
	skycoin.RootCmd.Use = "skycoin"
	skycoin.RootCmd.Short = "skycoin daemon & cli"
	skyhw.RootCmd.Use = "skyhw"
	skyhw.RootCmd.Short = "skycoin hardware wallet utilities"

	if fmt.Sprintf("%v", buildinfo.DebugBuildInfo()) != "" {
		RootCmd.Flags().BoolVarP(&di, "info", "d", false, "print runtime/debug.BuildInfo")
	}
	if fmt.Sprintf("%v", buildinfo.DBIVersion()) != "" {
		RootCmd.Flags().BoolVarP(&bv, "bv", "b", false, "print runtime/debug.BuildInfo.Main.Version")
	}
}

// RootCmd contains visor, cli, services, dmsg, apps, and skycoin utilities
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

var appsCmd = &cobra.Command{
	Use:   "app",
	Short: "skywire native applications",
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
