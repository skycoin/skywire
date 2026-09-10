//go:build js && wasm

// Package commands cmd/skywire/commands/deskhost_js.go c4-vis-cli
//
// `skywire desk-host`: the desk's DOM-side surfaces (pkg/wasmhv/deskhost) out
// of the ONE js/wasm build of this binary. desk-boot.js spawns it through the
// page's process layer as it spawns every other command — same module URL,
// same compile cache — so the desk page loads one module for the desk host,
// the tab's visor and each `skywire …` the terminal runs.
//
// It installs the surfaces and parks: returning would end the instance, and
// with it every function it published. It runs no visor and no autoconfig.
package commands

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/wasmhv/deskhost"
)

var deskHostRole string

func init() {
	deskHostCmd.Flags().StringVar(&deskHostRole, "role", "auto", "which surfaces to install: shell|browser|netview|cipher|auto")
	RootCmd.AddCommand(deskHostCmd)
}

var deskHostCmd = &cobra.Command{
	Use:    "desk-host",
	Short:  "run the desk's in-page surfaces (browser build only)",
	Hidden: true,
	Run: func(_ *cobra.Command, _ []string) {
		deskhost.Run(deskHostRole)
	},
}
