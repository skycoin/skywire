// Package commands cmd/skywire-cli/commands/shortcuts.go
//
// Top-level convenience commands. The verbs typed most often
// (pk / status / halt) live at the root so they don't need the
// `visor` middle word. The longer forms (cli visor pk, etc.)
// keep working unchanged.
//
// Each shortcut is a thin wrapper that delegates Run to the
// existing visor subcommand. The two commands share Run logic
// without sharing the *cobra.Command instance — that lets each
// have its own GroupID for the parent's help template, since
// root and `cli visor` use different group taxonomies.
//
// `hv` is intentionally not surfaced here: it has a subcommand
// tree that doesn't lift cleanly with this wrapper pattern.
// Promoting it is tracked as a follow-up.
package commands

import (
	"github.com/spf13/cobra"

	clivisor "github.com/skycoin/skywire/cmd/skywire-cli/commands/visor"
)

func init() {
	statusCmd.GroupID = groupVisor
	pkCmd.GroupID = groupVisor
	haltCmd.GroupID = groupVisor
	// Re-mount the dnslabel subcommand under the shortcut so
	// `cli pk dnslabel` works as well as `cli visor pk dnslabel`.
	// The shortcut's pkCmd is a separate *cobra.Command — its
	// children aren't inherited from the underlying visor pkCmd.
	pkCmd.AddCommand(clivisor.PKDNSLabelCmd)
}

// statusCmd is `skywire cli status` — equivalent to `cli visor info`.
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Summary of visor info (alias for `cli visor info`)",
	Long:  "\n  Summary of visor info — same as `skywire cli visor info`.",
	Run:   clivisor.SummaryCmd.Run,
}

// pkCmd is `skywire cli pk` — equivalent to `cli visor pk`.
var pkCmd = &cobra.Command{
	Use:   "pk",
	Short: "Public key of the visor (alias for `cli visor pk`)",
	Long:  "\n  Public key of the visor — same as `skywire cli visor pk`.",
	Run:   clivisor.PKCmd.Run,
}

// haltCmd is `skywire cli halt` — equivalent to `cli visor halt`.
var haltCmd = &cobra.Command{
	Use:   "halt",
	Short: "Stop a running visor (alias for `cli visor halt`)",
	Long:  "\n  Stop a running visor — same as `skywire cli visor halt`.",
	Run:   clivisor.HaltCmd.Run,
}
