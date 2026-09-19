// Package clivisor cmd/skywire-cli/commands/visor/hv_auth.go c4-vis-cli
package clivisor

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

var hvAuthPersist bool

func init() {
	hvCmd.AddCommand(hvAuthCmd)
	hvAuthCmd.Flags().BoolVarP(&hvAuthPersist, "persist", "w", false, "write change to config file")
}

var hvAuthCmd = &cobra.Command{
	Use:   "auth [on|off]",
	Short: "Whether the hypervisor UI requires a login, at runtime",
	Long: `Turn the hypervisor web UI's login requirement on or off without
restarting the visor. With no argument, print the current state.

hypervisor.enable_auth is read while the UI's HTTP router is BUILT,
so changing it rebuilds that router — the same cycle 'hv ui
disable' + 'hv ui enable' performs. The DMSG-RPC listener,
managed-visor tracking and 'hv ls' are untouched, and so is the
visor. Existing browser sessions do not survive the switch: reload
any tab open on the UI.

'off' removes the password prompt but does NOT delete the admin
account — turn auth back on and the same password applies. The UI
is unauthenticated while this is off, so bind it to loopback (or
use 'hv ui disable') on anything reachable from a network you do
not control.

Use -w to also write hypervisor.enable_auth to skywire-config.json,
so it survives a restart. That json is a derived artifact: a later
'skywire autoconfig' run (which a package install or update
performs) rebuilds it from /etc/skywire.conf and resets the change.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if len(args) == 0 {
			state := "off"
			if rpcClient.IsHypervisorAuthEnabled() {
				state = "on"
			}
			internal.PrintOutput(cmd.Flags(), state, fmt.Sprintf("Hypervisor UI auth: %s\n", state))
			return
		}
		var enable bool
		switch strings.ToLower(args[0]) {
		case "on", "true", "enable", "enabled", "1":
			enable = true
		case "off", "false", "disable", "disabled", "0":
			enable = false
		default:
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("expected 'on' or 'off', got %q", args[0]))
		}
		if err := rpcClient.SetHypervisorAuthPersist(enable, hvAuthPersist); err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		state := "off"
		if enable {
			state = "on"
		}
		msg := fmt.Sprintf("Hypervisor UI auth %s; reload any open tab.\n", state)
		if hvAuthPersist {
			msg = fmt.Sprintf("Hypervisor UI auth %s (written to config); reload any open tab.\n", state)
		}
		internal.PrintOutput(cmd.Flags(), msg, msg)
	},
}
