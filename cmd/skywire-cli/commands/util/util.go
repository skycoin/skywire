// Package cliutil cmd/skywire-cli/commands/util/util.go
package cliutil

import (
	"github.com/spf13/cobra"

	cliedit "github.com/skycoin/skywire/cmd/skywire-cli/commands/edit"
	clijq "github.com/skycoin/skywire/cmd/skywire-cli/commands/jq"
)

func init() {
	// Unhide commands when under util parent
	clijq.RootCmd.Hidden = false
	cliedit.RootCmd.Hidden = false

	RootCmd.AddCommand(
		clijq.RootCmd,
		cliedit.RootCmd,
		staticServeCmd,
	)
}

// RootCmd is the util command grouping standalone utilities.
var RootCmd = &cobra.Command{
	Use:   "util",
	Short: "Bundled utility commands",
}
