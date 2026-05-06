// Package cliskycoin provides convenience commands for managing
// skycoin-daemon and skycoin-web instances on a running visor. The
// underlying primitives — AddApp, SetAppEnv, SetAutoStart, StartApp,
// StopApp — are exposed as `skywire cli visor app *` subcommands;
// these wrappers chain the common workflows so operators don't have
// to script them by hand.
package cliskycoin

import (
	"github.com/spf13/cobra"
)

// RootCmd is wired into the main CLI tree from cmd/skywire-cli/commands/root.go.
var RootCmd = &cobra.Command{
	Use:   "skycoin",
	Short: "Manage skycoin-daemon / skycoin-web instances on the visor",
	Long: `Manage skycoin-daemon and skycoin-web instances on a running
visor — runtime equivalents of the SKYCOIND_* SKYENV knobs in
'skywire cli config gen'. Multi-instance daemons (one per
fibercoin) are added, configured, and started here without
editing skywire-config.json by hand.`,
}

func init() {
	RootCmd.AddCommand(daemonCmd)
	daemonCmd.AddCommand(
		daemonListCmd,
		daemonAddCmd,
		daemonStartCmd,
		daemonStopCmd,
	)
	daemonAddCmd.Flags().StringVar(&daemonAddFiberToml, "fiber-toml", "", "FIBER_TOML path (run a fibercoin chain instead of vanilla skycoin)")
	daemonAddCmd.Flags().IntVar(&daemonAddPort, "port", 0, "daemon HTTP port (0 = auto-allocate base 6420 + 2*N where N is the existing instance count)")
	daemonAddCmd.Flags().StringVar(&daemonAddDataDir, "data-dir", "", "daemon --data-dir override (empty = let the visor's launcher pick a per-instance default)")
	daemonAddCmd.Flags().StringVar(&daemonAddAPISets, "api-sets", "", "comma-separated --enable-gui-api-sets value (empty = daemon defaults)")
	daemonAddCmd.Flags().BoolVar(&daemonAddAutoStart, "autostart", false, "set auto_start on the new entry so it starts on visor boot")
	daemonAddCmd.Flags().BoolVar(&daemonAddStart, "start", false, "start the daemon immediately after configuring")
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "skycoin-daemon instances (full nodes)",
}
