// Package visorapi pkg/visor/visorapi/routing.go c3-vis-core
package visorapi

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

// RouterSettings bundles the visor-wide router knobs the universal
// "Router Settings" UI panel surfaces. Mirrors the per-flag CLI
// surface ('cli proxy start --local-route' etc.) so the UI can
// achieve parity in one round-trip rather than four separate calls.
type RouterSettings struct {
	ForceLocalRoutes bool   `json:"force_local_routes"`
	ExistingTPOnly   bool   `json:"existing_tp_only"`
	MinHops          uint16 `json:"min_hops"`
	// TransportPreference is the transport-type priority order: which type
	// is tried first when a transport has to be created, and which existing
	// one a route rides when several reach the same peer. GET always answers
	// the full order in effect. On PUT:
	//   omitted / null — leave the order unchanged;
	//   []             — revert to the built-in default;
	//   non-empty      — that order, most-preferred first (unlisted types
	//                    sort after it).
	// Written to routing.transport_preference, so it survives a restart —
	// but not a config regen (no skywire.conf field).
	TransportPreference []string `json:"transport_preference,omitempty"`

	// The mux send-window shape and the adaptive park hold, live from
	// pkg/router/settings.go. Each zero value means "leave unchanged" on PUT,
	// so an older client that does not know a field cannot reset it. GET always
	// answers the values in force.
	//
	// windowRefreshInterval is deliberately absent: it becomes a per-route-group
	// ticker when the group is built, so moving it live is a locking change
	// rather than a knob.
	EcfMaxWindowBytes int64         `json:"ecf_max_window_bytes,omitempty"`
	EcfMinWindowBytes int64         `json:"ecf_min_window_bytes,omitempty"`
	EcfWindowMargin   float64       `json:"ecf_window_margin,omitempty"`
	SendWindowWaitMax time.Duration `json:"send_window_wait_max,omitempty"`
	LegParkMinHold    time.Duration `json:"leg_park_min_hold,omitempty"`

	// The dead-route exclusion window and its ceiling, live from
	// pkg/router/settings.go with the same zero-means-unchanged rule.
	DeadRouteHold    time.Duration `json:"dead_route_hold,omitempty"`
	DeadRouteHoldMax time.Duration `json:"dead_route_hold_max,omitempty"`

	// Shared-bottleneck detection: how many per-leg delay samples a verdict
	// needs, and the minimum spacing between two per-SACK samples for one leg.
	// Same zero-means-unchanged rule.
	SBDMinSamples     int           `json:"sbd_min_samples,omitempty"`
	SBDSampleInterval time.Duration `json:"sbd_sample_interval,omitempty"`

	// The terms of the park TRIAL that qualifies a shared-bottleneck ruling: how
	// long the park is held before the aggregate goodput is re-read, the fraction
	// of goodput it may cost before it is undone, and the first exemption window
	// the vindicated pair earns. Same zero-means-unchanged rule.
	SBDTrialWindow time.Duration `json:"sbd_trial_window,omitempty"`
	SBDTrialLoss   float64       `json:"sbd_trial_loss,omitempty"`
	SBDBackoff     time.Duration `json:"sbd_backoff,omitempty"`

	// SBDMinEvidenceRate is the aggregate delivered-bytes rate (B/s) a group must
	// be carrying before a shared-bottleneck ruling may park one of its legs: no
	// traffic, no ruling. Same zero-means-unchanged rule.
	SBDMinEvidenceRate int64 `json:"sbd_min_evidence_rate,omitempty"`

	// ForwardSpill lets a FORWARD frame leave the confined leg when that leg is at
	// its send window (the pre-fix behavior). Off by default: the writer waits for
	// the window instead, because every live row that spilled an upload across two
	// skewed legs collapsed. Tri-state on PUT like SBDDemote — nil leaves it alone.
	ForwardSpill *bool `json:"forward_spill,omitempty"`

	// ForwardSwitchMargin is how much LOWER a challenger leg must measure before the
	// forward direction moves off the leg it holds (0.2 = 20 %), for two consecutive
	// samples. Same zero-means-unchanged rule.
	ForwardSwitchMargin float64 `json:"forward_switch_margin,omitempty"`

	// LegStarveRatio is how many times the best ready leg's delay basis a leg's
	// own must exceed before the scheduler cuts it to a probe per window, and
	// LegProbeBytes is that probe. Same zero-means-unchanged rule; a very large
	// ratio disables the gate.
	LegStarveRatio float64 `json:"leg_starve_ratio,omitempty"`
	LegProbeBytes  int64   `json:"leg_probe_bytes,omitempty"`

	// SBDDemote gates the DEMOTION half of shared-bottleneck detection: false (the
	// default) records every ruling as an sbd_ruling mux event and parks nothing.
	// Like MuxFEC it is a tri-state on PUT — nil leaves it alone — because a bool
	// has no "zero means unchanged" spelling.
	SBDDemote *bool `json:"sbd_demote,omitempty"`

	// MuxFEC advertises FEC on mux route groups created from now on; unlike
	// the rest it is a tri-state on PUT, see SetRouterSettings.
	MuxFEC *bool `json:"mux_fec,omitempty"`

	// Knobs is the whole catalog as one map of catalog-name to formatted value.
	// On PUT it is applied as a PARTIAL set — a knob the map does not name is
	// left alone — which is what lets a sweep move one value without restating
	// the other eighty. On GET it carries every knob's live value, so a bench
	// runner can save it and restore it verbatim.
	Knobs map[string]string `json:"knobs,omitempty"`

	// KnobApp scopes a PUT's Knobs to the route groups owned by one app rather
	// than the whole visor. Empty is the visor-wide set.
	KnobApp string `json:"knob_app,omitempty"`

	// KnobReset restores every knob to its compiled default and drops every
	// per-app override, before Knobs (if any) is applied. With KnobApp set it
	// drops only that app's overrides.
	KnobReset bool `json:"knob_reset,omitempty"`

	// KnobState is the GET-side detail: every knob with its live value, its
	// compiled default, whether it was explicitly set, and what is written in
	// the config — the persisted-vs-live comparison an operator needs before a
	// restart.
	KnobState []RouterKnob `json:"knob_state,omitempty"`

	// AppKnobs is every app's override map on GET, app name to knob map.
	AppKnobs map[string]map[string]string `json:"app_knobs,omitempty"`
}

// RouterKnob is one row of RouterSettings.KnobState.
type RouterKnob struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Value is the live value, formatted the way `route settings` takes it back.
	Value string `json:"value"`
	// Default is what the binary compiled with.
	Default string `json:"default"`
	// Set reports an explicit set, as opposed to a value that merely equals the
	// default.
	Set bool `json:"set"`
	// Persisted is what the visor config holds for this knob, empty when nothing
	// is written. A Persisted that differs from Value is a change that will be
	// lost — or gained — at the next restart.
	Persisted string `json:"persisted,omitempty"`
	Doc       string `json:"doc,omitempty"`
}

// RouterDialSettings bundles the visor-wide DIAL-TIME router knobs — the
// route-ranking priors, the candidate counts, the warm-plan cache shape, the
// dead-route young-death window, the dial-time tunnel width and the operator's
// prefer-these-peers list.
//
// It is a separate struct from RouterSettings (which carries the live-group
// knobs) so the two commands stay independently readable and a caller parsing
// `route settings dial --json` gets one document, not a merged one.
//
// On a SET, a zero field means "leave unchanged" — the same tri-state
// RouterSettings uses — except PreferPKs, where an explicitly empty
// (non-nil, length-0) slice clears the list. ClearPreferPKs makes that
// unambiguous over the wire, where nil and empty do not survive the round trip
// distinctly.
type RouterDialSettings struct {
	UnknownLatencyCostMs float64  `json:"unknown_latency_cost_ms"`
	UnknownHopPenaltyMs  float64  `json:"unknown_hop_penalty_ms"`
	TypePriorScale       float64  `json:"type_prior_scale"`
	ThroughputPriorScale float64  `json:"throughput_prior_scale"`
	RouteCandidates      int      `json:"route_candidates"`
	MuxRouteHeadroom     int      `json:"mux_route_headroom"`
	ForegroundMux        int      `json:"foreground_mux"`
	TunnelLegs           int      `json:"tunnel_legs"`
	DeadRouteYoungAgeMs  int64    `json:"dead_route_young_age_ms"`
	WarmPlanTTLMs        int64    `json:"warm_plan_ttl_ms"`
	WarmPlanBucketCap    int      `json:"warm_plan_bucket_cap"`
	PreferPKs            []string `json:"prefer_pks,omitempty"`
	ClearPreferPKs       bool     `json:"clear_prefer_pks,omitempty"`
	// TypePriorScaleSet / ThroughputPriorScaleSet mark the two multipliers as
	// given, since 0 is meaningful for them (it drops the term from the score)
	// and so cannot double as "unset".
	TypePriorScaleSet       bool `json:"type_prior_scale_set,omitempty"`
	ThroughputPriorScaleSet bool `json:"throughput_prior_scale_set,omitempty"`
	// TunnelLegsSet distinguishes "set tunnel legs to 0" (turn the dial-time
	// widening off) from "leave it alone", since 0 is both the default and a
	// meaningful instruction.
	TunnelLegsSet bool `json:"tunnel_legs_set,omitempty"`
}

// MuxWeightsInput is the argument of the SetMuxWeights / ClearMuxWeights /
// MuxWeights RPCs. Weights are keyed by the leg's first-hop transport id as a
// string, so the wire form matches what `mux info` prints and what an operator
// types.
type MuxWeightsInput struct {
	AppName string
	RGPort  uint16
	Weights map[string]float64
}

// MuxRehomeInput is the argument of the RehomeTunnelLeg RPC: the app, the
// ACTIVE tunnel that is to gain a leg, and the STANDBY tunnel whose chain it
// takes. Both ports are the dst_port `proxy mux info` prints.
type MuxRehomeInput struct {
	AppName     string
	TargetPort  uint16
	StandbyPort uint16
}

// GetRouterDialSettings implements API.
func (rc *rpcClient) GetRouterDialSettings() (RouterDialSettings, error) {
	var out RouterDialSettings
	if err := rc.Call("GetRouterDialSettings", &struct{}{}, &out); err != nil {
		return RouterDialSettings{}, err
	}
	return out, nil
}

// SetRouterDialSettings implements API.
func (rc *rpcClient) SetRouterDialSettings(s RouterDialSettings) error {
	return rc.Call("SetRouterDialSettings", &s, &struct{}{})
}

// SetMuxWeights implements API.
func (rc *rpcClient) SetMuxWeights(appName string, weights map[string]float64, rgPort uint16) (router.MuxWeightsView, error) {
	var out router.MuxWeightsView
	err := rc.Call("SetMuxWeights", &MuxWeightsInput{AppName: appName, RGPort: rgPort, Weights: weights}, &out)
	return out, err
}

// ClearMuxWeights implements API.
func (rc *rpcClient) ClearMuxWeights(appName string, rgPort uint16) (router.MuxWeightsView, error) {
	var out router.MuxWeightsView
	err := rc.Call("ClearMuxWeights", &MuxWeightsInput{AppName: appName, RGPort: rgPort}, &out)
	return out, err
}

// MuxWeights implements API.
func (rc *rpcClient) MuxWeights(appName string, rgPort uint16) (router.MuxWeightsView, error) {
	var out router.MuxWeightsView
	err := rc.Call("MuxWeights", &MuxWeightsInput{AppName: appName, RGPort: rgPort}, &out)
	return out, err
}

// AddMuxRouteForward implements API.
func (rc *rpcClient) AddMuxRouteForward(appName string, fwd, rev []routing.Hop, rgPort uint16) error {
	return rc.Call("AddMuxRouteForward", &MuxRouteInput{
		AppName: appName, Forward: fwd, Reverse: rev, SrcPort: rgPort,
	}, &struct{}{})
}

// RehomeTunnelLeg implements API.
func (rc *rpcClient) RehomeTunnelLeg(appName string, targetPort, standbyPort uint16) error {
	return rc.Call("RehomeTunnelLeg", &MuxRehomeInput{
		AppName: appName, TargetPort: targetPort, StandbyPort: standbyPort,
	}, &struct{}{})
}

// RouteGroupMuxNegotiated implements API.
func (rc *rpcClient) RouteGroupMuxNegotiated(appName string) ([]router.MuxNegotiated, error) {
	var out []router.MuxNegotiated
	err := rc.Call("RouteGroupMuxNegotiated", &appName, &out)
	return out, err
}

func (*mockRPCClient) GetRouterDialSettings() (RouterDialSettings, error) {
	return RouterDialSettings{}, nil
}

func (*mockRPCClient) SetRouterDialSettings(RouterDialSettings) error { return nil }

func (*mockRPCClient) SetMuxWeights(string, map[string]float64, uint16) (router.MuxWeightsView, error) {
	return router.MuxWeightsView{}, nil
}

func (*mockRPCClient) ClearMuxWeights(string, uint16) (router.MuxWeightsView, error) {
	return router.MuxWeightsView{}, nil
}

func (*mockRPCClient) MuxWeights(string, uint16) (router.MuxWeightsView, error) {
	return router.MuxWeightsView{}, nil
}

func (*mockRPCClient) AddMuxRouteForward(string, []routing.Hop, []routing.Hop, uint16) error {
	return nil
}

func (*mockRPCClient) RouteGroupMuxNegotiated(string) ([]router.MuxNegotiated, error) {
	return nil, nil
}

func (*mockRPCClient) RehomeTunnelLeg(string, uint16, uint16) error { return nil }

// RoutingPolicyInfo describes one installed engine for the UI.
type RoutingPolicyInfo struct {
	// Source is the path or inline marker the engine was
	// constructed from — file path, "<inline>", or "<noop>".
	Source string `json:"source"`

	// Active reports whether an evaluator is currently loaded
	// (false on a noop / failed-to-load engine).
	Active bool `json:"active"`

	// Backend is "skylark" or "wasm", derived from the source
	// file extension. Inline policies are always skylark.
	// Empty string when Source is "<noop>" / "<inline>" — UI
	// can fall back to Source for display.
	Backend string `json:"backend,omitempty"`
}

// RoutingPoliciesSummary is the JSON shape returned by
// /visors/{pk}/routing-policies. Default may be nil when no
// visor-wide policy is configured; PerApp is empty (not nil) so
// the JSON encodes as {} rather than null for missing data.
type RoutingPoliciesSummary struct {
	Default *RoutingPolicyInfo            `json:"default,omitempty"`
	PerApp  map[string]*RoutingPolicyInfo `json:"per_app"`
}

type LANDmsgServerInfo struct {
	Enabled       bool          `json:"enabled"`
	PK            cipher.PubKey `json:"pk,omitempty"`
	Address       string        `json:"address,omitempty"`        // LAN-routable "host:port" advertised by the hypervisor.
	PublicAddress string        `json:"public_address,omitempty"` // Operator-set WAN-routable "host:port"; empty when not configured.
	// DiscoveryURL is the hypervisor-hosted dmsg-discovery proxy URL that
	// receiving visors should use as their primary discovery (with their
	// existing public dmsg-discovery as fall-through behind it). Empty
	// when the hypervisor's HTTP address isn't remotely reachable AND the
	// operator hasn't set lan_dmsg_server.public_discovery_url. Visors
	// that receive a non-empty value save it to config; the change takes
	// effect on the next visor restart.
	DiscoveryURL string `json:"discovery_url,omitempty"`
}

// RoutingRuleResp is one routing rule as the summary and the hypervisor API
// report it: its route ID and a hex or summarized form of the rule.
type RoutingRuleResp struct {
	Key     routing.RouteID      `json:"key"`
	Rule    string               `json:"rule"`
	Summary *routing.RuleSummary `json:"rule_summary,omitempty"`
}
