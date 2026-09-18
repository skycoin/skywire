// Package cliroute pkg/cliout/cliroute/dial_settings.go
//
// Output shape of `skywire cli route settings dial` — the visor's DIAL-TIME
// router knobs. Separate from Settings (the live-group knobs) so each command
// emits one document a caller can parse without first splitting it.
package cliroute

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// DialSettings is the current value of every dial-time router knob.
type DialSettings struct {
	UnknownLatencyCostMs float64  `json:"unknown_latency_cost_ms"`
	UnknownHopPenaltyMs  float64  `json:"unknown_hop_penalty_ms"`
	TypePriorScale       float64  `json:"type_prior_scale"`
	ThroughputPriorScale float64  `json:"throughput_prior_scale"`
	RouteCandidates      int      `json:"route_candidates"`
	MuxRouteHeadroom     int      `json:"mux_route_headroom"`
	ForegroundMux        int      `json:"foreground_mux"`
	TunnelLegs           int      `json:"tunnel_legs"`
	DeadRouteYoungAge    string   `json:"dead_route_young_age"`
	WarmPlanTTL          string   `json:"warm_plan_ttl"`
	WarmPlanBucketCap    int      `json:"warm_plan_bucket_cap"`
	PreferPKs            []string `json:"prefer_pks"`
}

// Human prints one knob per row: its catalog name (what `route settings
// <key>=<value>` and the persisted routing.router_settings map use), the flag
// that also sets it, and the live value. The prefer-PK list has no catalog
// name — its payload is not one int64 — so its first column is blank.
func (s DialSettings) Human(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	rows := [][3]string{
		{"dial.unknown_latency_cost_ms", "--unknown-latency-cost-ms", fmt.Sprintf("%g", s.UnknownLatencyCostMs)},
		{"dial.unknown_hop_penalty_ms", "--unknown-hop-penalty-ms", fmt.Sprintf("%g", s.UnknownHopPenaltyMs)},
		{"dial.type_prior_scale", "--dial-type-prior-scale", fmt.Sprintf("%g", s.TypePriorScale)},
		{"dial.throughput_prior_scale", "--dial-throughput-prior-scale", fmt.Sprintf("%g", s.ThroughputPriorScale)},
		{"dial.candidates", "--dial-candidates", fmt.Sprint(s.RouteCandidates)},
		{"dial.candidate_headroom", "--dial-candidate-headroom", fmt.Sprint(s.MuxRouteHeadroom)},
		{"dial.foreground_mux", "--dial-foreground-mux", fmt.Sprint(s.ForegroundMux)},
		{"dial.tunnel_legs", "--dial-tunnel-legs", fmt.Sprint(s.TunnelLegs)},
		{"route.dead_young_age", "--dead-route-young-age", s.DeadRouteYoungAge},
		{"warm.plan_ttl", "--warm-plan-ttl", s.WarmPlanTTL},
		{"warm.plan_bucket_cap", "--warm-plan-bucket-cap", fmt.Sprint(s.WarmPlanBucketCap)},
		{"", "--dial-prefer-pks", strings.Join(s.PreferPKs, ",")},
	}
	for _, r := range rows {
		v := r[2]
		if v == "" {
			v = "(none)"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", r[0], r[1], v); err != nil {
			return err
		}
	}
	return tw.Flush()
}
