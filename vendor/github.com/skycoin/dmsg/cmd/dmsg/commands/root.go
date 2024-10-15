// Package commands cmd/dmsg/commands/root.go
package commands

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

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
)

func init() {
	dmsgptyCmd.AddCommand(
		dpc.RootCmd,
		dph.RootCmd,
		dpu.RootCmd,
	)
	RootCmd.AddCommand(
		dmsgptyCmd,
		dd.RootCmd,
		ds.RootCmd,
		dh.RootCmd,
		dc.RootCmd,
		dw.RootCmd,
		ds5.RootCmd,
		di.RootCmd,
	)
	dd.RootCmd.Use = "disc"
	ds.RootCmd.Use = "server"
	dh.RootCmd.Use = "http"
	dc.RootCmd.Use = "curl"
	dw.RootCmd.Use = "web"
	ds5.RootCmd.Use = "socks"
	dpc.RootCmd.Use = "cli"
	dph.RootCmd.Use = "host"
	dpu.RootCmd.Use = "ui"
	di.RootCmd.Use = "ip"
}

// RootCmd contains all binaries which may be separately compiled as subcommands
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "DMSG services & utilities",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐
	 │││││└─┐│ ┬
	─┴┘┴ ┴└─┘└─┘
DMSG services & utilities`,
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
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
