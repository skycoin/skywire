// Package skysocksc cmd/skywire-cli/commands/proxy/mux.go c4-vis-cli
package skysocksc

import (
	"strings"

	"github.com/spf13/cobra"
)

// muxCmd groups the mux'd-route-group operations for a proxy session under a
// single `proxy mux` parent, instead of a flat pile of `proxy mux-*` commands.
var muxCmd = &cobra.Command{
	Use:   "mux",
	Short: "Mux'd route-group operations for an active proxy session",
	Long: `Inspect and reshape the multiplexed route group behind a running
skysocks-client (or vpn-client) session:

  info   per-leg traffic for the active route group(s)
  set    reconcile the legs to a target set
  add    add a leg from a piped route
  rm     remove a leg by transport id
  mode   change the mux scheduler weighting at runtime
  auto   adapt legs off live latency to a preset`,
}

func init() {
	RootCmd.AddCommand(muxCmd)
}

// addMuxSub nests sub under the `mux` parent (with its short verb as Use) and
// preserves the old flat `proxy mux-<verb>` invocation as a hidden alias so
// existing scripts / muscle-memory keep working. The alias shares sub's Run and
// flag set (all bound to the same package-level vars), so there is no behavioral
// drift between the two forms. Call AFTER sub's flags are registered.
func addMuxSub(sub *cobra.Command, oldName string) {
	muxCmd.AddCommand(sub)

	alias := &cobra.Command{
		Use:                   oldName + useArgs(sub.Use),
		Short:                 sub.Short + " (deprecated: use `proxy mux " + firstWord(sub.Use) + "`)",
		Hidden:                true,
		Args:                  sub.Args,
		DisableFlagsInUseLine: sub.DisableFlagsInUseLine,
		Run:                   sub.Run,
		RunE:                  sub.RunE,
	}
	alias.Flags().AddFlagSet(sub.Flags())
	RootCmd.AddCommand(alias)
}

// firstWord returns the verb of a cobra Use line ("rm <tp-id>" -> "rm").
func firstWord(use string) string {
	if i := strings.IndexByte(use, ' '); i >= 0 {
		return use[:i]
	}
	return use
}

// useArgs returns the argument suffix of a Use line ("rm <tp-id>" -> " <tp-id>",
// "info" -> ""), so the alias keeps the same usage hint as the nested command.
func useArgs(use string) string {
	if i := strings.IndexByte(use, ' '); i >= 0 {
		return use[i:]
	}
	return ""
}
