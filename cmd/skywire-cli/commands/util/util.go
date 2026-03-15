// Package cliutil cmd/skywire-cli/commands/util/util.go
package cliutil

import (
	"github.com/spf13/cobra"

	cliedit "github.com/skycoin/skywire/cmd/skywire-cli/commands/edit"
	cligot "github.com/skycoin/skywire/cmd/skywire-cli/commands/got"
	clijq "github.com/skycoin/skywire/cmd/skywire-cli/commands/jq"
)

func init() {
	// Unhide commands when under util parent
	clijq.RootCmd.Hidden = false
	cliedit.RootCmd.Hidden = false

	RootCmd.AddCommand(
		clijq.RootCmd,
		cliedit.RootCmd,
		cligot.RootCmd,
	)
}

// RootCmd is the util command grouping standalone utilities.
var RootCmd = &cobra.Command{
	Use:   "util",
	Short: "Bundled utility commands",
	Long: `Standalone utilities bundled with skywire.

  util jq     jq-like JSON processor (gojq)
  util edit   Terminal text editor (femto)
  util got    HTTP client with concurrent downloads`,
}
