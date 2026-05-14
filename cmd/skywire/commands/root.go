// Package commands cmd/skywire/commands/root.go
package commands

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"

	sc "github.com/skycoin/skywire/cmd/apps/skychat/commands"
	snc "github.com/skycoin/skywire/cmd/apps/skynet-client/commands"
	sn "github.com/skycoin/skywire/cmd/apps/skynet/commands"
	ssc "github.com/skycoin/skywire/cmd/apps/skysocks-client/commands"
	ss "github.com/skycoin/skywire/cmd/apps/skysocks/commands"
	vpnc "github.com/skycoin/skywire/cmd/apps/vpn-client/commands"
	vpns "github.com/skycoin/skywire/cmd/apps/vpn-server/commands"
	cxo "github.com/skycoin/skywire/cmd/cxo/commands"
	dmsg "github.com/skycoin/skywire/cmd/dmsg/dmsg/commands"
	scli "github.com/skycoin/skywire/cmd/skywire-cli/commands"
	"github.com/skycoin/skywire/cmd/skywire/commands/doc"
	services "github.com/skycoin/skywire/cmd/svc/skywire-services/commands"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/flags"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	bv bool
	di bool
)

func init() {
	appsCmd.AddCommand(
		vpns.RootCmd,
		vpnc.RootCmd,
		ssc.RootCmd,
		ss.RootCmd,
		sc.RootCmd,
		sn.RootCmd,
		snc.RootCmd,
	)
	// Install flag-aware `help` (supports -r/-t/-d modes) on the
	// Install flag-aware `help` + coloredcobra styling on every
	// subcommand root that has its own subcommand tree. The top-
	// level skywire binary calls InitFlags on its own RootCmd (in
	// main()), but that doesn't recurse — without InitStyle here,
	// `skywire cli` renders without colors because cc.Init was never
	// run on scli.RootCmd.
	for _, sub := range []*cobra.Command{scli.RootCmd, services.RootCmd, dmsg.RootCmd, visor.RootCmd, cxo.RootCmd} {
		flags.InstallHelp(sub)
		flags.InitStyle(sub)
	}

	// cliutil is NOT added at this level — accessible via
	// `skywire cli util`. Adding it here forced a GroupID mirror
	// that leaked into the cli's own help layout. The top-level
	// menu stays flat (not enough subcommands to justify groups).
	RootCmd.AddCommand(
		visor.RootCmd,
		scli.RootCmd,
		services.RootCmd,
		dmsg.RootCmd,
		cxo.RootCmd,
		appsCmd,
		doc.RootCmd,
	)

	visor.RootCmd.Long = calvin.AsciiFont("skywire-visor")
	dmsg.RootCmd.Use = "dmsg"
	services.RootCmd.Use = "svc"

	scli.RootCmd.Use = "cli"
	visor.RootCmd.Use = "visor"
	vpns.RootCmd.Use = "vpn-server"
	vpnc.RootCmd.Use = "vpn-client"
	ssc.RootCmd.Use = "skysocks-client"
	ss.RootCmd.Use = "skysocks"
	sc.RootCmd.Use = "skychat"
	sn.RootCmd.Use = "skynet-srv"
	snc.RootCmd.Use = "skynet-client"

	// --all reveals hidden subcommands (e.g., autoconfig)
	RootCmd.Flags().BoolVar(&skyShowAll, "all", false, "show all subcommands (including hidden)")
	RootCmd.Flags().MarkHidden("all") //nolint:errcheck,gosec

	modifySubcommands(RootCmd)
	if fmt.Sprintf("%v", buildinfo.DebugBuildInfo()) != "" {
		RootCmd.Flags().BoolVarP(&di, "info", "d", false, "print runtime/debug.BuildInfo")
	}
	if fmt.Sprintf("%v", buildinfo.DBIVersion()) != "" {
		RootCmd.Flags().BoolVarP(&bv, "bv", "b", false, "print runtime/debug.BuildInfo.Main.Version")
	}
}

func modifySubcommands(cmd *cobra.Command) {
	for i := range cmd.Commands() {
		cmd.Commands()[i].Version = ""
		cmd.Commands()[i].SilenceErrors = true
		cmd.Commands()[i].SilenceUsage = true
		cmd.Commands()[i].DisableSuggestions = true
		cmd.Commands()[i].DisableFlagsInUseLine = true
		modifySubcommands(cmd.Commands()[i]) // recursion
	}
}

var skyShowAll bool

// RootCmd contains literally every 'command' from four repos here
var RootCmd = &cobra.Command{
	Use: "skywire",
	Long: func() (ret string) {
		ret = calvin.AsciiFont("skywire")
		if buildinfo.DBIVersion() != "" {
			ret += fmt.Sprintf("\n%v", buildinfo.DBIVersion())
		} else {
			ret += fmt.Sprintf("\nskywire version %v", buildinfo.Version())
		}
		if buildinfo.Go() != "unknown" && buildinfo.Go() != "" {
			ret += "\nbuilt with " + buildinfo.Go()
			if buildinfo.Date() != "unknown" && buildinfo.Date() != "" {
				ret += " on " + buildinfo.Date()
			}
		}
		return ret
	}(),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(cmd *cobra.Command, _ []string) {
		if skyShowAll {
			for _, sub := range cmd.Commands() {
				sub.Hidden = false
			}
			cmd.Flags().MarkHidden("help") //nolint:errcheck,gosec
			cmd.Help()                     //nolint:errcheck,gosec
			return
		}
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
	Long:  calvin.AsciiFont("apps"),
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
