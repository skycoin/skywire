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
	"github.com/skycoin/skywire/pkg/router/routersettings"
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
	settingsSBDMinN   int
	settingsSBDEvery  time.Duration
	settingsSBDTrialW time.Duration
	settingsSBDTrialL float64
	settingsSBDBackof time.Duration
	settingsSBDEvid   string
	settingsSBDDemote string
	settingsFwdSpill  string
	settingsFwdMargin float64
	settingsStarveRat float64
	settingsProbeByte string
	settingsApp       string
	settingsReset     bool
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
	settingsCmd.Flags().IntVar(&settingsSBDMinN, "sbd-min-samples", 0, "per-leg delay samples a shared-bottleneck verdict needs before it may park a leg")
	settingsCmd.Flags().DurationVar(&settingsSBDEvery, "sbd-sample-interval", 0, "minimum spacing between two per-SACK delay samples folded into a leg's shared-bottleneck window")
	settingsCmd.Flags().DurationVar(&settingsSBDTrialW, "sbd-trial-window", 0, "how long a shared-bottleneck park is held as a trial before the aggregate goodput is re-read")
	settingsCmd.Flags().Float64Var(&settingsSBDTrialL, "sbd-trial-loss", 0, "fraction of aggregate goodput a park may cost before it is undone (e.g. 0.15)")
	settingsCmd.Flags().DurationVar(&settingsSBDBackof, "sbd-backoff", 0, "how long a pair whose park trial failed is exempt from shared-bottleneck merging (doubles per repeat)")
	settingsCmd.Flags().StringVar(&settingsSBDEvid, "sbd-min-evidence-rate", "", "aggregate goodput a group must carry before a shared-bottleneck ruling may park a leg (e.g. 64KiB)")
	settingsCmd.Flags().StringVar(&settingsSBDDemote, "sbd-demote", "", "true|false: let a shared-bottleneck ruling PARK a leg (default false — rulings are recorded as sbd_ruling mux events only)")
	settingsCmd.Flags().StringVar(&settingsFwdSpill, "forward-spill", "", "true|false: let a FORWARD frame leave its confined leg when that leg is at its send window (default false — the writer waits instead)")
	settingsCmd.Flags().Float64Var(&settingsFwdMargin, "forward-switch-margin", 0, "how much lower a challenger leg must measure, for two consecutive samples, before the forward direction moves to it (e.g. 0.2)")
	settingsCmd.Flags().Float64Var(&settingsStarveRat, "leg-starve-ratio", 0, "delay-basis multiple AND inverse goodput fraction at which a leg is cut to a probe per window (e.g. 6.0)")
	settingsCmd.Flags().StringVar(&settingsProbeByte, "leg-probe-bytes", "", "what a leg cut to probe-only may carry per window (e.g. 64KiB)")
	settingsCmd.Flags().StringVar(&settingsApp, "app", "", "scope the key=value settings to the route groups owned by this app (default: visor-wide)")
	settingsCmd.Flags().BoolVar(&settingsReset, "reset", false, "restore every knob to the value the binary compiled with (with --app: drop that app's overrides)")
}

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Show or set the visor's runtime router knobs",
	Long: `Show or set the visor's runtime router knobs.

With no arguments every knob is printed: the routing choices (minimum hops,
existing-transports-only, local route calculation, transport type preference)
followed by the whole mux/dataplane catalog — send windows, RACK and reorder
terms, leg liveness and failover timers, SACK and delayed-ack cadence, TLP and
HoL retransmit, shared-bottleneck detection, FEC geometry, the ECF selector,
the unidirectional flip controller, the mux event rings, and the capability
toggles. --json carries the same catalog as a stable map under "knobs", plus
each knob's compiled default and what the config holds for it under
"knob_detail", so a bench runner can save it and feed it back verbatim.

Knobs are set as key=value arguments, one or many:

  skywire cli route settings ecf.max_window_bytes=16MiB rack.ceil=800ms
  skywire cli route settings mux.sack=false
  skywire cli route settings --app skysocks-client leg.starve_ratio=3
  skywire cli route settings --reset

Values take the same spellings 'proxy settings' accepts: 8MiB / 64K for bytes,
250ms / 5s for durations, a plain float for ratios, true/false for toggles.
Every default is the value the binary compiled with, so a visor that sets
nothing behaves exactly as it did.

--app scopes the change to the route groups owned by that app, so a subject
client and its paired reference can run different values on one visor;
unspecified knobs fall back to the visor-wide value. --reset restores every
default (with --app, drops just that app's overrides).

Changes take effect at once: values read on a data path are live, and the ones
read when a route group is BUILT — the negotiated capabilities mux.per_frame_noise,
mux.sack, mux.hol_retx and fec.enabled — apply to NEW groups, leaving groups
already running untouched. Everything set here is written to
routing.router_settings and restored at the next start. The older per-knob flags
below remain for compatibility; they name the same knobs.`,
	Args: cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		cur, err := rpcClient.GetRouterSettings()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		// key=value arguments are the catalog surface; the per-knob flags below are
		// the older names for the same values.
		knobs := map[string]string{}
		for _, a := range args {
			k, val, ok := strings.Cut(a, "=")
			k, val = strings.TrimSpace(k), strings.TrimSpace(val)
			if !ok || k == "" || val == "" {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("%q is not key=value", a))
			}
			if _, err := routersettings.Parse(k, val); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			knobs[k] = val
		}
		changed := false
		for _, f := range []string{"prefer", "min-hops", "existing-tp-only", "force-local",
			"ecf-max-window", "ecf-min-window", "ecf-window-margin", "send-window-wait-max",
			"leg-park-min-hold", "dead-route-hold", "dead-route-hold-max", "mux-fec",
			"sbd-min-samples", "sbd-sample-interval", "sbd-trial-window", "sbd-trial-loss", "sbd-backoff",
			"sbd-min-evidence-rate", "sbd-demote", "forward-spill", "forward-switch-margin",
			"leg-starve-ratio", "leg-probe-bytes"} {
			changed = changed || cmd.Flags().Changed(f)
		}
		changed = changed || len(knobs) > 0 || settingsReset
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
			if cmd.Flags().Changed("sbd-min-samples") {
				next.SBDMinSamples = settingsSBDMinN
			}
			if cmd.Flags().Changed("sbd-sample-interval") {
				next.SBDSampleInterval = settingsSBDEvery
			}
			if cmd.Flags().Changed("sbd-trial-window") {
				next.SBDTrialWindow = settingsSBDTrialW
			}
			if cmd.Flags().Changed("sbd-trial-loss") {
				next.SBDTrialLoss = settingsSBDTrialL
			}
			if cmd.Flags().Changed("sbd-backoff") {
				next.SBDBackoff = settingsSBDBackof
			}
			if cmd.Flags().Changed("sbd-min-evidence-rate") {
				next.SBDMinEvidenceRate = mustBytes(cmd, settingsSBDEvid)
			}
			if cmd.Flags().Changed("sbd-demote") {
				demote := settingsSBDDemote == "true"
				next.SBDDemote = &demote
			}
			if cmd.Flags().Changed("forward-spill") {
				spill := settingsFwdSpill == "true"
				next.ForwardSpill = &spill
			}
			if cmd.Flags().Changed("forward-switch-margin") {
				next.ForwardSwitchMargin = settingsFwdMargin
			}
			if cmd.Flags().Changed("leg-starve-ratio") {
				next.LegStarveRatio = settingsStarveRat
			}
			if cmd.Flags().Changed("leg-probe-bytes") {
				next.LegProbeBytes = mustBytes(cmd, settingsProbeByte)
			}
			next.Knobs = knobs
			next.KnobApp = settingsApp
			next.KnobReset = settingsReset
			if err := rpcClient.SetRouterSettings(next); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
			if cur, err = rpcClient.GetRouterSettings(); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
		}
		fec := cur.MuxFEC != nil && *cur.MuxFEC
		sbdDemote := cur.SBDDemote != nil && *cur.SBDDemote
		fwdSpill := cur.ForwardSpill != nil && *cur.ForwardSpill
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
			SBDMinSamples:       cur.SBDMinSamples,
			SBDSampleInterval:   cur.SBDSampleInterval.String(),
			SBDTrialWindow:      cur.SBDTrialWindow.String(),
			SBDTrialLoss:        cur.SBDTrialLoss,
			SBDBackoff:          cur.SBDBackoff.String(),
			SBDMinEvidenceRate:  cur.SBDMinEvidenceRate,
			SBDDemote:           sbdDemote,
			ForwardSpill:        fwdSpill,
			ForwardSwitchMargin: cur.ForwardSwitchMargin,
			LegStarveRatio:      cur.LegStarveRatio,
			LegProbeBytes:       cur.LegProbeBytes,
			Knobs:               cur.Knobs,
			KnobDetail:          knobDetail(cur.KnobState),
			AppKnobs:            cur.AppKnobs,
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

// knobDetail turns the RPC's knob rows into the map the JSON output carries:
// per knob the compiled default, whether it was explicitly set, and what the
// visor config holds — the persisted-vs-live comparison.
func knobDetail(rows []visor.RouterKnob) map[string]cliroute.Knob {
	if len(rows) == 0 {
		return nil
	}
	out := make(map[string]cliroute.Knob, len(rows))
	for _, r := range rows {
		out[r.Name] = cliroute.Knob{
			Kind:      r.Kind,
			Value:     r.Value,
			Default:   r.Default,
			Set:       r.Set,
			Persisted: r.Persisted,
			Doc:       r.Doc,
		}
	}
	return out
}
