// Package skysocksc cmd/skywire-cli/commands/proxy/mux_ops.go
//
// Runtime mux reconfiguration commands. The visor exposes
// AddMuxRoute / RemoveMuxRoute / SetMuxMode RPCs already; these
// commands surface them so users can reconfigure an active proxy
// session without stopping and restarting.
//
// Workflow:
//
//	skywire cli proxy mux-info               # see current legs
//	skywire cli proxy mux-add <tp-id>        # bring up a new leg
//	skywire cli proxy mux-rm  <tp-id>        # drop a leg
//	skywire cli proxy mux-mode auto|equal    # change scheduler
//
// Combined with 'mux-info --watch' in a second terminal, this gives
// you the basic interactive loop for exploring mux behavior at
// runtime.
package skysocksc

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

var (
	muxOpsApp string
)

func init() {
	muxAddCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose route group to modify")
	muxRmCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose route group to modify")
	RootCmd.AddCommand(muxAddCmd)
	RootCmd.AddCommand(muxRmCmd)
	RootCmd.AddCommand(muxModeCmd)
}

var muxAddCmd = &cobra.Command{
	Use:   "mux-add <tp-id>",
	Short: "Add a leg to an active proxy session's mux'd route group",
	Long: `Add a new mux leg routed via the specified transport.

The transport must already exist (see 'skywire cli tp ls' for the
local transport set, or 'skywire cli tp all' for network-wide). The
visor builds a route through that transport and appends it to the
named app's active route group, after which the mux scheduler will
start picking it according to the current mode.

Idempotent — adding a transport that's already a leg is a no-op.

Example:
  skywire cli proxy mux-info        # see current legs
  skywire cli proxy mux-add 55d43098-bae7-029e-bd8e-b228f7208930
  skywire cli proxy mux-info        # confirm it appeared`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		tpID, err := uuid.Parse(args[0])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid transport id %q: %w", args[0], err))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if err := rpcClient.AddMuxRoute(muxOpsApp, tpID); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("AddMuxRoute: %w", err))
		}
		fmt.Printf("added mux leg via transport %s on app=%s\n", tpID, muxOpsApp)
	},
}

var muxRmCmd = &cobra.Command{
	Use:   "mux-rm <tp-id>",
	Short: "Remove a leg from an active proxy session's mux'd route group",
	Long: `Remove the mux leg routed via the specified transport.

The mux scheduler will stop selecting that leg immediately; in-flight
packets already on it complete normally. Removing the last leg in a
mux group leaves the group with the primary route only — to fully
tear down the session, use 'proxy stop' instead.

Example:
  skywire cli proxy mux-info                            # find the leg
  skywire cli proxy mux-rm 55d43098-bae7-029e-bd8e-b228f7208930`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		tpID, err := uuid.Parse(args[0])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid transport id %q: %w", args[0], err))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if err := rpcClient.RemoveMuxRoute(muxOpsApp, tpID); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("RemoveMuxRoute: %w", err))
		}
		fmt.Printf("removed mux leg via transport %s on app=%s\n", tpID, muxOpsApp)
	},
}

var muxModeCmd = &cobra.Command{
	Use:   "mux-mode <auto|equal>",
	Short: "Change mux scheduler weighting at runtime",
	Long: `Set the mux transport-selection mode for the visor.

  auto    - latency-weighted: lower-latency legs get more packets.
            Best when the legs have different RTTs (the typical case)
            because it minimizes head-of-line stalls in SACK reorder.
  equal   - round-robin: each leg gets equal share. Useful when legs
            have similar latency and you want to verify aggregation
            behavior without the auto-mode masking it.

Affects every active and future mux'd route group on this visor.
The setting persists to skywire-config.json so it survives restart.

Example:
  skywire cli proxy mux-mode equal      # before measuring aggregation
  skywire cli proxy mux-info --watch 1s
  skywire cli proxy mux-mode auto       # back to weighted`,
	Args:                  cobra.ExactArgs(1),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		mode := args[0]
		if mode != "auto" && mode != "equal" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("mode must be 'auto' or 'equal', got %q", mode))
		}
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if err := rpcClient.SetMuxMode(mode); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("SetMuxMode: %w", err))
		}
		fmt.Printf("mux mode set to %s\n", mode)
	},
}
