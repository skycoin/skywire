// Package skysocksc cmd/skywire-cli/commands/proxy/mux_ops.go
//
// Runtime mux reconfiguration commands. The visor exposes
// AddMuxRoute / RemoveMuxRoute / SetMuxMode RPCs already; these
// commands surface them so users can reconfigure an active proxy
// session without stopping and restarting.
//
// Workflow:
//
//	skywire cli proxy mux-info                # see current legs
//	skywire cli proxy mux-add                 # add a disjoint leg
//	skywire cli proxy mux-rm  <tp-id>         # drop a leg
//	skywire cli proxy mux-mode auto|equal     # change scheduler
//
// When the named app has multiple concurrent rg's (e.g. one per
// active SOCKS5 client connection on skysocks-client), use --rg
// <src-port> to pick which one. 'mux-info' prints the src_port
// for every rg so you can copy it across.
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
	muxOpsApp     string
	muxOpsSrcPort uint16
)

func init() {
	muxAddCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose route group to modify")
	muxAddCmd.Flags().Uint16Var(&muxOpsSrcPort, "rg", 0, "rg disambiguator: ephemeral src_port from 'mux-info' (only needed when the app has multiple active rg's)")
	muxRmCmd.Flags().StringVarP(&muxOpsApp, "name", "n", "skysocks-client", "app whose route group to modify")
	muxRmCmd.Flags().Uint16Var(&muxOpsSrcPort, "rg", 0, "rg disambiguator: ephemeral src_port from 'mux-info' (only needed when the app has multiple active rg's)")
	RootCmd.AddCommand(muxAddCmd)
	RootCmd.AddCommand(muxRmCmd)
	RootCmd.AddCommand(muxModeCmd)
}

var muxAddCmd = &cobra.Command{
	Use:   "mux-add",
	Short: "Add a transport-disjoint leg to an active proxy session's mux'd rg",
	Long: `Add a mux leg over a transport that the rg isn't already using.

The visor asks the route finder for any path between this visor and
the rg's peer that doesn't reuse a transport already in the group;
the new leg is appended and the mux scheduler starts selecting it
according to the current mode.

No transport-id argument: pinning a specific transport rarely yields
real path diversity (two direct transports to the same peer share the
physical link). For the "I want a leg through THIS specific
intermediate" case, compute the route off-router via 'route calc'
and dial it as a separate session.

When the app has multiple concurrent rg's, pass --rg <src-port> to
target one of them; otherwise the visor errors with the candidate
list.

Example:
  skywire cli proxy mux-info        # see current legs + rg src_port
  skywire cli proxy mux-add         # add another disjoint leg
  skywire cli proxy mux-info        # confirm it appeared`,
	Args:                  cobra.NoArgs,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("unable to create RPC client: %w", err))
		}
		defer rpcClient.Close() //nolint:errcheck,gosec

		if err := rpcClient.AddMuxRoute(muxOpsApp, muxOpsSrcPort); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("AddMuxRoute: %w", err))
		}
		fmt.Printf("added disjoint mux leg on app=%s\n", muxOpsApp)
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

When the app has multiple concurrent rg's, pass --rg <src-port> to
target one of them; otherwise the visor errors with the candidate
list.

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

		if err := rpcClient.RemoveMuxRoute(muxOpsApp, tpID, muxOpsSrcPort); err != nil {
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
