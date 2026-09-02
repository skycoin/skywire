//go:build js && wasm

// Package commands cmd/skycoin/commands/root_js.go c4-app-wallet
//
// Browser (js/wasm) stand-in for the `skywire skycoin` command group. The
// skycoin daemon/cli/explorer/newcoin trees carry the block database (bbolt
// through skycoin's own module path) and node plumbing that has no place in a
// browser build; this skeleton keeps the group present — same Use/Short/
// banner, same subcommand names — with a clear error on execution. The wallet
// itself is not lost in the browser: the wasm-visor serves the thin-client
// web wallet through the hypervisor UI.
package commands

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
)

const skycoinModulePath = "github.com/skycoin/skycoin"

// RootCmd is the `skywire skycoin` command group.
var RootCmd = &cobra.Command{
	Use:                   "skycoin",
	Short:                 "skycoin daemon & cli",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		if err := cmd.Help(); err != nil {
			log.Printf("Failed to print help: %v", err)
		}
	},
}

func stubCmd(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:                   use,
		Short:                 short,
		SilenceUsage:          true,
		DisableFlagsInUseLine: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("skycoin %s: the skycoin node tree (block database, daemon plumbing) is not available in the browser build", use)
		},
	}
}

func init() {
	long := calvin.AsciiFont("skycoin")
	if v := buildinfo.DepVersion(skycoinModulePath); v != "" {
		long += "\n" + v
		RootCmd.Version = v
	}
	if goVer := buildinfo.Go(); goVer != "" && goVer != "unknown" {
		long += "\nbuilt with " + goVer
	}
	RootCmd.Long = long

	RootCmd.AddCommand(
		stubCmd("daemon", "skycoin daemon"),
		stubCmd("web", "skycoin thin client web wallet"),
		stubCmd("cli", "skycoin command line interface"),
		stubCmd("newcoin", "newcoin utility"),
		stubCmd("explorer", "skycoin explorer"),
	)
}
