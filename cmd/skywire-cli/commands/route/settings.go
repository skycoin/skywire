// Package cliroute cmd/skywire-cli/commands/route/settings.go
package cliroute

import (
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/cliroute"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	settingsPrefer   string
	settingsMinHops  int
	settingsExisting string
	settingsLocal    string
)

func init() {
	RootCmd.AddCommand(settingsCmd)
	settingsCmd.Flags().StringVar(&settingsPrefer, "prefer", "", "transport type order, most preferred first (e.g. squicr,stcpr,sudph); 'default' restores the built-in order")
	settingsCmd.Flags().IntVar(&settingsMinHops, "min-hops", -1, "minimum hops for route calculation")
	settingsCmd.Flags().StringVar(&settingsExisting, "existing-tp-only", "", "true|false: only route over transports that already exist")
	settingsCmd.Flags().StringVar(&settingsLocal, "force-local", "", "true|false: calculate routes locally instead of the route finder")
}

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Show or set the visor's runtime router knobs",
	Long: `Show or set the visor-wide router knobs: minimum hops, existing-transports-only,
local route calculation, and the transport type preference — which type is
created first and which existing transport a direct route rides when several
reach the same peer. With no flags the current values are printed. Changes take
effect at once; the preference is written to routing.transport_preference.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		cur, err := rpcClient.GetRouterSettings()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if cmd.Flags().Changed("prefer") || cmd.Flags().Changed("min-hops") ||
			cmd.Flags().Changed("existing-tp-only") || cmd.Flags().Changed("force-local") {
			next := visor.RouterSettings{
				ForceLocalRoutes: cur.ForceLocalRoutes,
				ExistingTPOnly:   cur.ExistingTPOnly,
				MinHops:          cur.MinHops,
			}
			if cmd.Flags().Changed("min-hops") {
				next.MinHops = uint16(settingsMinHops) //nolint:gosec
			}
			if cmd.Flags().Changed("existing-tp-only") {
				next.ExistingTPOnly = settingsExisting == "true"
			}
			if cmd.Flags().Changed("force-local") {
				next.ForceLocalRoutes = settingsLocal == "true"
			}
			if cmd.Flags().Changed("prefer") {
				if settingsPrefer == "default" {
					for _, t := range types.DefaultPreferenceOrder() {
						next.TransportPreference = append(next.TransportPreference, string(t))
					}
				} else {
					next.TransportPreference = strings.Split(settingsPrefer, ",")
				}
			}
			if err := rpcClient.SetRouterSettings(next); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			if cur, err = rpcClient.GetRouterSettings(); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliroute.Settings{
			ForceLocalRoutes:    cur.ForceLocalRoutes,
			ExistingTPOnly:      cur.ExistingTPOnly,
			MinHops:             cur.MinHops,
			TransportPreference: cur.TransportPreference,
		}))
	},
}
