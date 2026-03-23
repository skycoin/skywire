// Package commands cmd/dmsg/commands/root.go
package commands

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/spf13/cobra"

	df "github.com/skycoin/dmsg/cmd/conf/commands"
	dl "github.com/skycoin/dmsg/cmd/dial/commands"
	dd "github.com/skycoin/dmsg/cmd/dmsg-discovery/commands"
	ds "github.com/skycoin/dmsg/cmd/dmsg-server/commands"
	ds5 "github.com/skycoin/dmsg/cmd/dmsg-socks5/commands"
	dc "github.com/skycoin/dmsg/cmd/dmsgcurl/commands"
	dh "github.com/skycoin/dmsg/cmd/dmsghttp/commands"
	di "github.com/skycoin/dmsg/cmd/dmsgip/commands"
	dpc "github.com/skycoin/dmsg/cmd/dmsgpty-cli/commands"
	dph "github.com/skycoin/dmsg/cmd/dmsgpty-host/commands"
	dpu "github.com/skycoin/dmsg/cmd/dmsgpty-ui/commands"
	dw "github.com/skycoin/dmsg/cmd/dmsgweb/commands"
	"github.com/skycoin/dmsg/pkg/dmsgclient"
)

var (
	bv  bool
	dbi bool
)

func init() {
	dmsgptyCmd.AddCommand(
		dpc.RootCmd,
		dph.RootCmd,
		dpu.RootCmd,
	)

	ds.RootCmd.AddCommand(
		dl.RootCmd,
	)
	RootCmd.AddCommand(
		dmsgptyCmd,
		dd.RootCmd,
		ds.RootCmd,
		df.RootCmd,
		dh.RootCmd,
		dc.RootCmd,
		dw.RootCmd,
		ds5.RootCmd,
		di.RootCmd,
	)
	dd.RootCmd.Use = "disc"
	ds.RootCmd.Use = "server"
	dl.RootCmd.Use = "dial"
	df.RootCmd.Use = "conf"
	dh.RootCmd.Use = "http"
	dc.RootCmd.Use = "curl"
	dw.RootCmd.Use = "web"
	ds5.RootCmd.Use = "socks"
	dpc.RootCmd.Use = "cli"
	dph.RootCmd.Use = "host"
	dpu.RootCmd.Use = "ui"
	di.RootCmd.Use = "ip"

	modifySubcommands(RootCmd)
	if fmt.Sprintf("%v", buildinfo.DebugBuildInfo()) != "" {
		RootCmd.Flags().BoolVarP(&dbi, "info", "d", false, "print runtime/debug.BuildInfo")
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

// RootCmd contains all binaries which may be separately compiled as subcommands
var RootCmd = &cobra.Command{
	Use:   dmsgclient.ExecName(),
	Short: "DMSG services & utilities",
	Long: func() (ret string) {
		ret = calvin.AsciiFont("dmsg")
		if buildinfo.DBIVersion() != "" {
			ret += fmt.Sprintf("\n%v", buildinfo.DBIVersion())
		} else {
			ret += fmt.Sprintf("\nversion %v", buildinfo.Version())
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
}

var dmsgptyCmd = &cobra.Command{
	Use:   "pty",
	Short: "DMSG pseudoterminal (pty)",
	Long: `
	┌─┐┌┬┐┬ ┬
	├─┘ │ └┬┘
	┴   ┴  ┴
DMSG pseudoterminal (pty)`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}
