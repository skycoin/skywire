// Package cliroute cmd/skywire-cli/commands/route/settings.go
package cliroute

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/cliroute"
	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	settingsPrefer    string
	settingsMinHops   int
	settingsExisting  string
	settingsLocal     string
	settingsEcfMax    string
	settingsEcfMin    string
	settingsEcfMargin float64
	settingsSendWait  time.Duration
	settingsParkHold  time.Duration
	settingsDeadHold  time.Duration
	settingsDeadMax   time.Duration
	settingsMuxFEC    string
)

func init() {
	RootCmd.AddCommand(settingsCmd)
	settingsCmd.Flags().StringVar(&settingsPrefer, "prefer", "", "transport type order, most preferred first (e.g. squicr,stcpr,sudph); 'default' restores the built-in order")
	settingsCmd.Flags().IntVar(&settingsMinHops, "min-hops", -1, "minimum hops for route calculation")
	settingsCmd.Flags().StringVar(&settingsExisting, "existing-tp-only", "", "true|false: only route over transports that already exist")
	settingsCmd.Flags().StringVar(&settingsLocal, "force-local", "", "true|false: calculate routes locally instead of the route finder")
	settingsCmd.Flags().StringVar(&settingsEcfMax, "ecf-max-window", "", "per-leg send window ceiling (e.g. 8MiB)")
	settingsCmd.Flags().StringVar(&settingsEcfMin, "ecf-min-window", "", "per-leg send window floor (e.g. 128KiB)")
	settingsCmd.Flags().Float64Var(&settingsEcfMargin, "ecf-window-margin", 0, "multiplier on proven delivery-per-RTT (e.g. 2.0)")
	settingsCmd.Flags().DurationVar(&settingsSendWait, "send-window-wait-max", 0, "how long a writer parks when every ready leg is at its window")
	settingsCmd.Flags().DurationVar(&settingsParkHold, "leg-park-min-hold", 0, "how long an adaptive park holds before a leg may be re-admitted")
	settingsCmd.Flags().DurationVar(&settingsDeadHold, "dead-route-hold", 0, "how long a route that died young is kept out of the next diversify search")
	settingsCmd.Flags().DurationVar(&settingsDeadMax, "dead-route-hold-max", 0, "ceiling on the doubling applied to that window on each repeat death")
	settingsCmd.Flags().StringVar(&settingsMuxFEC, "mux-fec", "", "true|false: advertise FEC on NEW mux route groups")
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
		changed := false
		for _, f := range []string{"prefer", "min-hops", "existing-tp-only", "force-local",
			"ecf-max-window", "ecf-min-window", "ecf-window-margin", "send-window-wait-max",
			"leg-park-min-hold", "dead-route-hold", "dead-route-hold-max", "mux-fec"} {
			changed = changed || cmd.Flags().Changed(f)
		}
		if changed {
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
			if cmd.Flags().Changed("ecf-max-window") {
				next.EcfMaxWindowBytes = mustBytes(cmd, settingsEcfMax)
			}
			if cmd.Flags().Changed("ecf-min-window") {
				next.EcfMinWindowBytes = mustBytes(cmd, settingsEcfMin)
			}
			if cmd.Flags().Changed("ecf-window-margin") {
				next.EcfWindowMargin = settingsEcfMargin
			}
			if cmd.Flags().Changed("send-window-wait-max") {
				next.SendWindowWaitMax = settingsSendWait
			}
			if cmd.Flags().Changed("leg-park-min-hold") {
				next.LegParkMinHold = settingsParkHold
			}
			if cmd.Flags().Changed("dead-route-hold") {
				next.DeadRouteHold = settingsDeadHold
			}
			if cmd.Flags().Changed("dead-route-hold-max") {
				next.DeadRouteHoldMax = settingsDeadMax
			}
			if cmd.Flags().Changed("mux-fec") {
				fec := settingsMuxFEC == "true"
				next.MuxFEC = &fec
			}
			if err := rpcClient.SetRouterSettings(next); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			if cur, err = rpcClient.GetRouterSettings(); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
		}
		fec := cur.MuxFEC != nil && *cur.MuxFEC
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliroute.Settings{
			ForceLocalRoutes:    cur.ForceLocalRoutes,
			ExistingTPOnly:      cur.ExistingTPOnly,
			MinHops:             cur.MinHops,
			TransportPreference: cur.TransportPreference,
			EcfMaxWindowBytes:   cur.EcfMaxWindowBytes,
			EcfMinWindowBytes:   cur.EcfMinWindowBytes,
			EcfWindowMargin:     cur.EcfWindowMargin,
			SendWindowWaitMax:   cur.SendWindowWaitMax.String(),
			LegParkMinHold:      cur.LegParkMinHold.String(),
			DeadRouteHold:       cur.DeadRouteHold.String(),
			DeadRouteHoldMax:    cur.DeadRouteHoldMax.String(),
			MuxFEC:              fec,
		}))
	},
}

// mustBytes parses a byte size the way `proxy settings` does, so the two
// commands accept the same spellings (8MiB, 131072).
func mustBytes(cmd *cobra.Command, raw string) int64 {
	v, err := skysettings.Parse(skysettings.ChunkMaxBytes, raw)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("%q is not a byte size: %w", raw, err))
	}
	return v
}
