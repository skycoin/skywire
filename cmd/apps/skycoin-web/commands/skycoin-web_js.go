//go:build js && wasm

// Package commands cmd/apps/skycoin-web/commands/skycoin-web_js.go c4-app-wallet
//
// Browser (js/wasm) stand-in. The vendored skycoin-web SERVER command drags
// skycoin's readable/visor node types (and through them the block database),
// which have no place in a browser build. The wallet is not lost there: the
// browser visor serves the thin-client wallet through the hypervisor UI
// (pkg/visor's skycoin-web/src/gui embed), which compiles for js on its own.
package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// RootCmd is the `skywire app skycoin` command group.
var RootCmd = &cobra.Command{
	Use:   "skycoin",
	Short: "skycoin apps",
}

func init() {
	RootCmd.AddCommand(&cobra.Command{
		Use:                   "web",
		Short:                 "skycoin thin client web wallet",
		SilenceUsage:          true,
		DisableFlagsInUseLine: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("skycoin web: the standalone wallet server is not available in the browser build — the hypervisor UI serves the wallet instead")
		},
	})
}
