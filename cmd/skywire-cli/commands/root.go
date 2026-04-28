// Package commands cmd/skywire-cli/commands/root.go
package commands

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clicompletion "github.com/skycoin/skywire/cmd/skywire-cli/commands/completion"
	cliconfig "github.com/skycoin/skywire/cmd/skywire-cli/commands/config"
	clidmsg "github.com/skycoin/skywire/cmd/skywire-cli/commands/dmsg"
	cligotop "github.com/skycoin/skywire/cmd/skywire-cli/commands/gotop"
	clilog "github.com/skycoin/skywire/cmd/skywire-cli/commands/log"
	climdisc "github.com/skycoin/skywire/cmd/skywire-cli/commands/mdisc"
	cliskysocksc "github.com/skycoin/skywire/cmd/skywire-cli/commands/proxy"
	clipv "github.com/skycoin/skywire/cmd/skywire-cli/commands/pv"
	cliresolver "github.com/skycoin/skywire/cmd/skywire-cli/commands/resolver"
	clireward "github.com/skycoin/skywire/cmd/skywire-cli/commands/reward"
	clirewards "github.com/skycoin/skywire/cmd/skywire-cli/commands/rewards"
	clirg "github.com/skycoin/skywire/cmd/skywire-cli/commands/rg"
	cliroute "github.com/skycoin/skywire/cmd/skywire-cli/commands/route"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	clisd "github.com/skycoin/skywire/cmd/skywire-cli/commands/sd"
	cliserve "github.com/skycoin/skywire/cmd/skywire-cli/commands/serve"
	cliskychat "github.com/skycoin/skywire/cmd/skywire-cli/commands/skychat"
	cliskynet "github.com/skycoin/skywire/cmd/skywire-cli/commands/skynet"
	clisurvey "github.com/skycoin/skywire/cmd/skywire-cli/commands/survey"
	clisvc "github.com/skycoin/skywire/cmd/skywire-cli/commands/svc"
	clitp "github.com/skycoin/skywire/cmd/skywire-cli/commands/tp"
	clitps "github.com/skycoin/skywire/cmd/skywire-cli/commands/tps"
	cliut "github.com/skycoin/skywire/cmd/skywire-cli/commands/ut"
	cliutil "github.com/skycoin/skywire/cmd/skywire-cli/commands/util"
	clivisor "github.com/skycoin/skywire/cmd/skywire-cli/commands/visor"
	clivpn "github.com/skycoin/skywire/cmd/skywire-cli/commands/vpn"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/flags"
)

// Command groups for the root help output. Each GroupID must match
// the string assigned to a subcommand's GroupID below. Cobra sorts
// commands alphabetically within each group automatically.
const (
	groupConfig    = "config"
	groupVisor     = "visor"
	groupApps      = "apps"
	groupNet       = "net"
	groupDiscovery = "discovery"
	groupRewards   = "rewards"
	groupUtil      = "util"
)

func init() {
	RootCmd.AddGroup(
		&cobra.Group{ID: groupConfig, Title: "Configuration:"},
		&cobra.Group{ID: groupVisor, Title: "Visor:"},
		&cobra.Group{ID: groupApps, Title: "Apps:"},
		&cobra.Group{ID: groupNet, Title: "Networking:"},
		&cobra.Group{ID: groupDiscovery, Title: "Discovery & lookups:"},
		&cobra.Group{ID: groupRewards, Title: "Rewards:"},
		&cobra.Group{ID: groupUtil, Title: "Utilities:"},
	)

	// Assign each subcommand to its group. Keeping this as an explicit
	// table rather than editing every subpackage's RootCmd keeps the
	// group taxonomy in one place and makes it trivial to re-shuffle.
	cliconfig.RootCmd.GroupID = groupConfig

	clivisor.RootCmd.GroupID = groupVisor
	cligotop.RootCmd.GroupID = groupVisor
	cliresolver.RootCmd.GroupID = groupVisor
	cliserve.RootCmd.GroupID = groupNet

	clivpn.RootCmd.GroupID = groupApps
	cliskysocksc.RootCmd.GroupID = groupApps
	cliskychat.RootCmd.GroupID = groupApps
	cliskynet.RootCmd.GroupID = groupApps

	clitp.RootCmd.GroupID = groupNet
	clitps.RootCmd.GroupID = groupNet
	cliroute.RootCmd.GroupID = groupNet
	clirg.RootCmd.GroupID = groupNet
	clidmsg.RootCmd.GroupID = groupNet

	clisd.RootCmd.GroupID = groupDiscovery
	climdisc.RootCmd.GroupID = groupDiscovery
	cliut.RootCmd.GroupID = groupDiscovery
	clipv.RootCmd.GroupID = groupDiscovery
	clisvc.RootCmd.GroupID = groupDiscovery

	clireward.RootCmd.GroupID = groupRewards
	clirewards.RootCmd.GroupID = groupRewards
	clilog.RootCmd.GroupID = groupRewards
	clisurvey.RootCmd.GroupID = groupRewards

	cliutil.RootCmd.GroupID = groupUtil

	// Install flag-aware `help` command (supports -r/-t/-d). Covers
	// the case where `skywire cli` is invoked as a subcommand of the
	// combined `skywire` binary; the standalone `skywire-cli` binary
	// installs it separately via its own main.InitFlags call.
	flags.InstallHelp(RootCmd)

	RootCmd.AddCommand(
		cliconfig.RootCmd,
		clidmsg.RootCmd,
		cligotop.RootCmd,
		clivisor.RootCmd,
		clivpn.RootCmd,
		cliut.RootCmd,
		cliskynet.RootCmd,
		clireward.RootCmd,
		clirewards.RootCmd,
		clisurvey.RootCmd,
		cliroute.RootCmd,
		clirg.RootCmd,
		clitp.RootCmd,
		clitps.RootCmd,
		climdisc.RootCmd,
		clisd.RootCmd,
		cliskychat.RootCmd,
		clicompletion.RootCmd,
		clilog.RootCmd,
		cliskysocksc.RootCmd,
		cliresolver.RootCmd,
		cliserve.RootCmd,
		clipv.RootCmd,
		clisvc.RootCmd,
		cliutil.RootCmd,

		// Top-level shortcuts: high-traffic verbs reachable without
		// the `visor` middle word. Long forms keep working at
		// `cli visor pk`, `cli visor info`, `cli visor halt`.
		pkCmd,
		statusCmd,
		haltCmd,
	)
	var jsonOutput bool
	RootCmd.PersistentFlags().BoolVar(&jsonOutput, internal.JSONString, false, "print output as JSON")
	RootCmd.PersistentFlags().IntVar(&clirpc.Timeout, "timeout", 30, "RPC timeout in seconds (0 = unlimited)")
	RootCmd.PersistentFlags().MarkHidden("timeout") //nolint:errcheck,gosec

	// --all reveals hidden flags and subcommands
	RootCmd.Flags().BoolVar(&cliShowAll, "all", false, "show all flags and subcommands (including hidden)")
	RootCmd.Flags().MarkHidden("all") //nolint:errcheck,gosec
}

var cliShowAll bool

// cliHiddenFlags are persistent flags hidden by default, revealed by --all.
var cliHiddenFlags = []string{"timeout"}

// RootCmd is the root command for skywire-cli
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short:                 "Command Line Interface for skywire",
	Long:                  calvin.AsciiFont("skywire-cli"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	PreRun: func(cmd *cobra.Command, _ []string) {
		if cliShowAll {
			for _, name := range cliHiddenFlags {
				if f := cmd.Root().PersistentFlags().Lookup(name); f != nil {
					f.Hidden = false
				}
			}
			for _, sub := range cmd.Root().Commands() {
				sub.Hidden = false
			}
			cmd.Flags().MarkHidden("help") //nolint:errcheck,gosec
			cmd.Help()                     //nolint:errcheck,gosec
			os.Exit(0)
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
