// Package commands cmd/dmsg/commands/root.go
package commands

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	df "github.com/skycoin/skywire/cmd/dmsg/conf/commands"
	dl "github.com/skycoin/skywire/cmd/dmsg/dial/commands"
	dd "github.com/skycoin/skywire/cmd/dmsg/dmsg-discovery/commands"
	ds "github.com/skycoin/skywire/cmd/dmsg/dmsg-server/commands"
	ds5 "github.com/skycoin/skywire/cmd/dmsg/dmsg-socks5/commands"
	dc "github.com/skycoin/skywire/cmd/dmsg/dmsgcurl/commands"
	dh "github.com/skycoin/skywire/cmd/dmsg/dmsghttp/commands"
	di "github.com/skycoin/skywire/cmd/dmsg/dmsgip/commands"
	dw "github.com/skycoin/skywire/cmd/dmsg/dmsgweb/commands"
	dsp "github.com/skycoin/skywire/cmd/dmsg/self-ping/commands"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
)

var (
	bv       bool
	dbi      bool
	withKill bool
)

func init() {
	// pty subcommands (dmsgpty-cli / -host / -ui) moved to the
	// unified `skywire app pty <mode>` tree in cmd/apps/pty/commands.
	// `skywire dmsg pty <cli|host|ui>` is no longer a valid path;
	// operators should use `skywire app pty <exec|dmsg|tcp|http>`.

	ds.RootCmd.AddCommand(
		dl.RootCmd,
	)
	RootCmd.AddCommand(
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


// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}
