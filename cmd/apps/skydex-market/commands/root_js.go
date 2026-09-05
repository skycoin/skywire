//go:build js && wasm

// Package commands cmd/apps/skydex-market/commands/root_js.go
//
// Browser (js/wasm) stand-in for the skydex-market wrapper. The market engine
// stores its book in SQLite (modernc.org/sqlite → a userland libc that does
// not build for GOOS=js), so the wasm build carries only the command SKELETON:
// identical Use/Short/Long/flags — the help text stays a faithful mirror of
// the native binary — with a Run that explains the app cannot run here.
package commands

import (
	"fmt"

	"github.com/0magnet/calvin"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	dbPath string
	port   uint16
	uiAddr string
)

func init() {
	RootCmd.Flags().StringVar(&dbPath, "db", "", "path to SQLite database file (default: <work-dir>/skydex-market.db)")
	RootCmd.Flags().Uint16Var(&port, "port", 0, "routing port for communication between app and visor")
	RootCmd.Flags().StringVar(&uiAddr, "addr", skyenv.SkydexMarketAddr, "address to serve the operator UI on")
}

// RootCmd is the root command for skydex-market.
var RootCmd = &cobra.Command{
	Use:                   "skydex-market",
	Short:                 "SkyDEX - Market (Skywire decentralized exchange backend)",
	Long:                  calvin.AsciiFont("skydex-market"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	RunE: func(_ *cobra.Command, _ []string) error {
		return fmt.Errorf("skydex-market: the market engine needs its SQLite store — not available in the browser build")
	},
}

// Execute executes root CLI command.
func Execute() {
	cmdutil.RunRoot(RootCmd)
}
