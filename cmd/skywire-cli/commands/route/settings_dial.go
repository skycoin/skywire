// Package cliroute cmd/skywire-cli/commands/route/settings_dial.go
//
// `skywire cli route settings dial` — the DIAL-TIME half of the router's
// runtime knobs: the route-ranking priors, how many candidates a dial asks the
// finder for, the warm-plan cache shape, the dead-route young-death window, the
// dial-time tunnel width and the operator's prefer-these-peers list.
//
// It is a subcommand of `route settings` rather than more flags on it because
// the two sets answer different questions and a caller parsing --json should
// get one document per question, not a merged one.
package cliroute

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/cliroute"
	"github.com/skycoin/skywire/pkg/visor"
)

var (
	dialUnknownLatency float64
	dialUnknownHop     float64
	dialTypeScale      float64
	dialTputScale      float64
	dialCandidates     int
	dialHeadroom       int
	dialForegroundMux  int
	dialTunnelLegs     int
	dialDeadYoung      time.Duration
	dialWarmTTL        time.Duration
	dialWarmCap        int
	dialPreferPKs      string
)

// dialSettingsFlags are the flag names this command owns; a change to any of
// them turns the invocation from a read into a write.
var dialSettingsFlags = []string{
	"unknown-latency-cost-ms", "unknown-hop-penalty-ms",
	"dial-type-prior-scale", "dial-throughput-prior-scale",
	"dial-candidates", "dial-candidate-headroom", "dial-foreground-mux",
	"dial-tunnel-legs", "dead-route-young-age", "warm-plan-ttl",
	"warm-plan-bucket-cap", "dial-prefer-pks",
}

func init() {
	settingsCmd.AddCommand(dialSettingsCmd)
	f := dialSettingsCmd.Flags()
	f.Float64Var(&dialUnknownLatency, "unknown-latency-cost-ms", 0, "what a hop with NO latency measurement costs a candidate's score (default 1000)")
	f.Float64Var(&dialUnknownHop, "unknown-hop-penalty-ms", 0, "what one unmeasured hop costs a PARTIALLY measured path (default 150)")
	f.Float64Var(&dialTypeScale, "dial-type-prior-scale", -1, "multiplier on the transport-TYPE ranking prior; 0 ranks on RTT alone (default 1.0)")
	f.Float64Var(&dialTputScale, "dial-throughput-prior-scale", -1, "multiplier on the MEASURED-throughput ranking band; 0 ranks on RTT alone (default 1.0)")
	f.IntVar(&dialCandidates, "dial-candidates", 0, "floor on how many routes a mux dial asks the route finder for (default 3)")
	f.IntVar(&dialHeadroom, "dial-candidate-headroom", 0, "extra routes requested on top of the mux degree, so the disjoint pick can still reach its target (default 2)")
	f.IntVar(&dialForegroundMux, "dial-foreground-mux", 0, "how many mux legs are established SYNCHRONOUSLY at dial time (default 16)")
	f.IntVar(&dialTunnelLegs, "dial-tunnel-legs", 0, "legs an app tunnel is dialed with: 0 = what the app asked for (default), -1 = the visor's mux width, n = n legs")
	f.DurationVar(&dialDeadYoung, "dead-route-young-age", 0, "how soon after dial a route's death counts as evidence it is dead (default 12s)")
	f.DurationVar(&dialWarmTTL, "warm-plan-ttl", 0, "staleness bound on a cached disjoint route plan (default 30s)")
	f.IntVar(&dialWarmCap, "warm-plan-bucket-cap", 0, "how many distinct disjoint plans the warm pool holds per exit (default 64)")
	f.StringVar(&dialPreferPKs, "dial-prefer-pks", "", "comma-separated public keys; a candidate route through one of them ranks FIRST, above carrier class and latency. 'none' clears the list")
}

var dialSettingsCmd = &cobra.Command{
	Use:   "dial",
	Short: "Show or set the visor's dial-time route-ranking knobs",
	Long: `Show or set the DIAL-TIME router knobs — what a dial reads while it is
choosing a route, as opposed to what a live route group reads while it is
sending (those are 'route settings' itself).

This is a VIEW over one section of the same knob catalog: every value below is
a catalog entry, so it can equally be set as 'route settings <key>=<value>',
read back under "knobs"/"knob_detail" in 'route settings --json', restored by
'route settings --reset', and is written to routing.router_settings so it
survives a restart. The first column of the output is the catalog name.

  skywire cli route settings dial --dial-candidates 6
  skywire cli route settings dial.candidates=6            # the same knob

dial.tunnel_legs is also per-app scopeable, because a dial is the one place in
this set that knows whose tunnel it is:

  skywire cli route settings --app skysocks-client dial.tunnel_legs=3

With no flags the current values are printed. Every default IS the constant the
router compiled with, so an untouched visor ranks exactly as it did.

--dial-prefer-pks is the answer to "auto-diversify cannot prefer the routes that
won": name the intermediates a measured set proved out and every later dial
takes a route through one of them first, with carrier class and latency then
deciding among those. 'none' clears it. It is the one member of this set that is
NOT a catalog knob — a list of public keys is not an int64 payload — so it is
runtime-only and has no key= form.

Examples:
  skywire cli route settings dial
  skywire cli route settings dial --dial-candidates 6 --dial-candidate-headroom 4
  skywire cli route settings dial --dial-prefer-pks 0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb,03f57e7cf26c0764c5ab659a606add056ddf8bfad4f5bc7e8613cad05e5f228adf
  skywire cli route settings dial --dial-tunnel-legs -1   # dial tunnels at the visor's mux width
  skywire cli route settings dial --via dmsg://<exit pk>  # same knobs on the far end`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		changed := false
		for _, name := range dialSettingsFlags {
			changed = changed || cmd.Flags().Changed(name)
		}
		if changed {
			var next visor.RouterDialSettings
			if cmd.Flags().Changed("unknown-latency-cost-ms") {
				next.UnknownLatencyCostMs = dialUnknownLatency
			}
			if cmd.Flags().Changed("unknown-hop-penalty-ms") {
				next.UnknownHopPenaltyMs = dialUnknownHop
			}
			// The two scales carry an explicit "given" bit: 0 is a meaningful
			// value for them (it drops the term from the score), so it cannot
			// double as "unset".
			if cmd.Flags().Changed("dial-type-prior-scale") {
				next.TypePriorScale, next.TypePriorScaleSet = dialTypeScale, true
			}
			if cmd.Flags().Changed("dial-throughput-prior-scale") {
				next.ThroughputPriorScale, next.ThroughputPriorScaleSet = dialTputScale, true
			}
			if cmd.Flags().Changed("dial-candidates") {
				next.RouteCandidates = dialCandidates
			}
			if cmd.Flags().Changed("dial-candidate-headroom") {
				next.MuxRouteHeadroom = dialHeadroom
			}
			if cmd.Flags().Changed("dial-foreground-mux") {
				next.ForegroundMux = dialForegroundMux
			}
			if cmd.Flags().Changed("dial-tunnel-legs") {
				next.TunnelLegs = dialTunnelLegs
				next.TunnelLegsSet = true
			}
			if cmd.Flags().Changed("dead-route-young-age") {
				next.DeadRouteYoungAgeMs = dialDeadYoung.Milliseconds()
			}
			if cmd.Flags().Changed("warm-plan-ttl") {
				next.WarmPlanTTLMs = dialWarmTTL.Milliseconds()
			}
			if cmd.Flags().Changed("warm-plan-bucket-cap") {
				next.WarmPlanBucketCap = dialWarmCap
			}
			if cmd.Flags().Changed("dial-prefer-pks") {
				if dialPreferPKs == "" || dialPreferPKs == "none" {
					next.ClearPreferPKs = true
				} else {
					next.PreferPKs = splitCommaList(dialPreferPKs)
				}
			}
			if err := rpcClient.SetRouterDialSettings(next); err != nil {
				internal.PrintFatalError(cmd.Flags(), err)
			}
		}
		cur, err := rpcClient.GetRouterDialSettings()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.Catch(cmd.Flags(), cliout.Print(cmd, cliroute.DialSettings{
			UnknownLatencyCostMs: cur.UnknownLatencyCostMs,
			UnknownHopPenaltyMs:  cur.UnknownHopPenaltyMs,
			TypePriorScale:       cur.TypePriorScale,
			ThroughputPriorScale: cur.ThroughputPriorScale,
			RouteCandidates:      cur.RouteCandidates,
			MuxRouteHeadroom:     cur.MuxRouteHeadroom,
			ForegroundMux:        cur.ForegroundMux,
			TunnelLegs:           cur.TunnelLegs,
			DeadRouteYoungAge:    (time.Duration(cur.DeadRouteYoungAgeMs) * time.Millisecond).String(),
			WarmPlanTTL:          (time.Duration(cur.WarmPlanTTLMs) * time.Millisecond).String(),
			WarmPlanBucketCap:    cur.WarmPlanBucketCap,
			PreferPKs:            cur.PreferPKs,
		}))
	},
}

// splitCommaList splits an operator-typed comma list, dropping empty entries so
// a trailing comma is not a parse error the operator has to hunt for.
func splitCommaList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
