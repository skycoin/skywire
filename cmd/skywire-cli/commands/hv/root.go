// Package clihv cmd/skywire-cli/commands/hv/root.go c4-vis-cli
package clihv

import (
	"github.com/spf13/cobra"
)

// RootCmd is the `hv` command group: tools for the hypervisor UI, including the
// standalone single-file generator.
var RootCmd = &cobra.Command{
	Use:   "hv",
	Short: "Hypervisor UI tools",
}

func init() {
	RootCmd.AddCommand(genCmd)
}
