// Package commands cmd/dmsg/commands/root.go
package commands

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/spf13/cobra"

	df "github.com/skycoin/skywire/cmd/dmsg-commands/conf/commands"
	dl "github.com/skycoin/skywire/cmd/dmsg-commands/dial/commands"
	dd "github.com/skycoin/skywire/cmd/dmsg-commands/dmsg-discovery/commands"
	ds "github.com/skycoin/skywire/cmd/dmsg-commands/dmsg-server/commands"
	ds5 "github.com/skycoin/skywire/cmd/dmsg-commands/dmsg-socks5/commands"
	dc "github.com/skycoin/skywire/cmd/dmsg-commands/dmsgcurl/commands"
	dh "github.com/skycoin/skywire/cmd/dmsg-commands/dmsghttp/commands"
	di "github.com/skycoin/skywire/cmd/dmsg-commands/dmsgip/commands"
	dpc "github.com/skycoin/skywire/cmd/dmsg-commands/dmsgpty-cli/commands"
	dph "github.com/skycoin/skywire/cmd/dmsg-commands/dmsgpty-host/commands"
	dpu "github.com/skycoin/skywire/cmd/dmsg-commands/dmsgpty-ui/commands"
	dsp "github.com/skycoin/skywire/cmd/dmsg-commands/self-ping/commands"
	dw "github.com/skycoin/skywire/cmd/dmsg-commands/dmsgweb/commands"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
)

var (
	bv       bool
	dbi      bool
	withKill bool
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
		dsp.RootCmd,
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
	dsp.RootCmd.Use = "self-ping"

	modifySubcommands(RootCmd)
	RootCmd.PersistentFlags().BoolVar(&withKill, "with-kill", true, "force exit after 3 interrupt signals")
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
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		if withKill {
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt, syscall.SIGTERM)
			go func() {
				sigCount := 0
				for range c {
					sigCount++
					if sigCount >= 3 {
						os.Exit(1)
					}
				}
			}()
		}
	},
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
