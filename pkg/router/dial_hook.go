// Package router pkg/router/dial_hook.go — operator-programmable
// routing policy integration point. The router calls a configured
// DialHook before each DialRoutes invocation; the hook can adjust
// the per-dial knobs (MuxRoutes, MinHops) or refuse the dial.
//
// The hook is optional. With no hook configured (Config.DialHook
// is nil), DialRoutes behaves identically to its pre-integration
// shape. This keeps the integration zero-cost when no operator
// policy is in play.
//
// The hook is failure-safe: any error from BeforeDial is treated
// as "no adjustment" and dialing proceeds with the caller's
// original opts. A buggy or panicking policy cannot break
// dialing.
package router

import (
	"context"
	"strconv"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// DialHook is called by DialRoutes before route setup. Returns
// adjustments that the router applies to the dial's opts.
//
// Implementations live in pkg/router/policy and elsewhere. The
// interface here keeps pkg/router free of the Starlark machinery.
type DialHook interface {
	// BeforeDial is invoked synchronously before route setup. Its
	// return value adjusts the dial's opts in-place. Returning the
	// zero DialAdjustment + nil error means "no override; proceed
	// with the caller's opts unchanged."
	//
	// Implementations MUST return quickly — DialRoutes blocks on
	// this call before the route-finder query begins. Budget is
	// the same as the policy's per-call timeout (default 50ms).
	BeforeDial(ctx context.Context, info DialInfo) (DialAdjustment, error)
}

// DialInfo summarizes the dial for the hook. It's a flat struct
// rather than passing DialOptions so the hook stays decoupled
// from DialOptions' field churn.
type DialInfo struct {
	AppName string
	PeerPK  cipher.PubKey
	LPort   routing.Port
	RPort   routing.Port

	// CLIOverrides surfaces any DialOptions values the caller
	// (CLI / app) populated before BeforeDial fires, so the
	// script can honor or override them. Keys mirror DialOptions
	// field names in snake_case (mux_routes, min_hops,
	// forward_mux_routes, etc.). Only non-zero values appear.
	// The policy's adjustment runs AFTER this is captured, so
	// the script gets a true "what did the operator pass" view.
	CLIOverrides map[string]string
}

// DialAdjustment is the hook's return value. Each field is a
// "override this if non-zero" hint; zero values mean "no change
// for this knob." Fallback can be set to "drop" to refuse the
// dial entirely.
type DialAdjustment struct {
	// MuxRoutes, when > 0, overrides DialOptions.MuxRoutes for
	// this dial.
	MuxRoutes int

	// ForwardMuxRoutes and ReverseMuxRoutes are per-direction
	// overrides for asymmetric mux. Zero falls back to MuxRoutes.
	// Used by policies that want different upstream / downstream
	// fan-out (e.g. ForwardMuxRoutes=1 + ReverseMuxRoutes=4 for
	// a download-heavy workload).
	ForwardMuxRoutes int
	ReverseMuxRoutes int

	// MinHops, when > 0, overrides DialOptions.MinHops for this
	// dial.
	MinHops int

	// ForwardMinHops and ReverseMinHops are per-direction
	// overrides for MinHops. Zero falls back to MinHops.
	ForwardMinHops int
	ReverseMinHops int

	// Fallback drives the no-overlay paths:
	//   "drop"   — refuse the dial with ErrDialPolicyDropped.
	//   "direct" — skip the route-finder, force the direct-
	//              transport short-circuit (UseExistingTpOnly=true
	//              + MinHops=1). If no direct transport exists,
	//              DialRoutes still fails — the script gets the
	//              all-or-nothing semantic it asked for.
	// Any other value is ignored (the dial proceeds normally).
	Fallback string

	// Distribution, when Mode != DistributionUnset, overrides
	// the route group's mux distribution strategy (which by
	// default tracks the router's visor-wide muxMode). The
	// policy layer parses the operator's descriptor string
	// (Starlark RouteSpec.Distribution) into this struct so
	// the router stays free of parser logic.
	Distribution DistributionConfig
}

// DistributionConfig drives the per-packet leg-selection
// strategy for a mux-enabled route group. Set on DialAdjustment
// (per-dial, from a routing-policy script) or on DialOptions
// (per-call from a CLI flag); zero-value (Mode == DistributionUnset)
// means "no override — use the router's visor-wide muxMode."
//
// See pkg/router/policy/distribution.go for the descriptor
// grammar that policy scripts emit.
type DistributionConfig struct {
	// Mode selects the per-packet algorithm. DistributionUnset
	// means "no override."
	Mode DistributionMode

	// Weights are the operator-supplied fractional weights for
	// DistributionWeighted. Must be the same length as the route
	// group's leg count; values are normalized into an integer
	// schedule by the selector. Empty for non-weighted modes.
	Weights []float64

	// SizeThreshold is the payload-size boundary for
	// DistributionSizeThreshold. Packets larger than this go to
	// leg 0 (assumed to be the wider pipe — the first leg the
	// route-finder returned, ranked by latency); smaller packets
	// round-robin across the remaining legs.
	SizeThreshold int
}

// DistributionMode enumerates the per-packet distribution
// strategies the operator can pick from. Names match the
// vocabulary the RFC's Starlark descriptor parses to.
type DistributionMode int

const (
	// DistributionUnset means "no policy override." The route
	// group uses the router's visor-wide muxMode (currently
	// WeightModeAuto: latency-weighted with round-robin fallback).
	DistributionUnset DistributionMode = iota
	// DistributionRoundRobin distributes packets evenly across
	// all legs, ignoring latency. Same effect as the existing
	// WeightModeEqual.
	DistributionRoundRobin
	// DistributionAuto picks WeightModeAuto explicitly — useful
	// when a script wants to override an operator's globally-
	// configured Equal mode back to latency-weighted for one app.
	DistributionAuto
	// DistributionWeighted uses the operator-supplied fractional
	// weights in Weights[]. Length must match the leg count.
	DistributionWeighted
	// DistributionSizeThreshold routes packets by payload size:
	// > SizeThreshold goes to leg 0, ≤ SizeThreshold round-robins
	// across the rest. Useful for bulk + control mixes (VPN,
	// terminal multiplexing) where large packets should claim
	// the wider pipe and small packets stay on the low-latency
	// leg.
	DistributionSizeThreshold
)

// LegChangeHook is an optional hook fired by the route group
// whenever its set of mux legs mutates — a new leg added via
// the establishment loop or appendRouteToGroup, or a leg dropping
// due to a transport close. The hook can return a new
// DistributionConfig to re-balance the per-packet selector against
// the updated leg count; returning the zero value
// (Mode == DistributionUnset) leaves the existing distribution in
// place.
//
// The route group calls OnLegChange synchronously from its leg-
// mutation paths so the schedule rebuild stays consistent with
// the leg set. Implementations must return quickly — the call
// budget mirrors the dial-time policy timeout (step ceiling).
type LegChangeHook interface {
	OnLegChange(info DialInfo, legs []LegInfo, change LegChange) DistributionConfig
}

// LegInfo describes one mux leg's current state. Index is the
// position in the route group's tps[] slice; Kind is the transport
// type ("stcpr" / "sudph" / "stcp" / "dmsg" — though DMSG is
// filtered out of mux groups at setup so it shouldn't appear here).
// LatencyMs is the most recent measurement, 0 for "unknown."
// Alive reflects whether the transport is currently serving (not
// closed / nil).
type LegInfo struct {
	Index     int
	Kind      string
	LatencyMs int
	Alive     bool
}

// LegChange describes the mutation that triggered the OnLegChange
// callback. Event is "added" or "dropped"; LegIndex is the leg
// that changed (the new leg's index for "added", the dropped
// leg's last-known index for "dropped").
type LegChange struct {
	Event    string
	LegIndex int
}

// String returns the stable label for a DistributionMode. Used
// by router-side log fields so operator-facing log lines say
// "round-robin" instead of "1".
func (m DistributionMode) String() string {
	switch m {
	case DistributionUnset:
		return "unset"
	case DistributionRoundRobin:
		return "round-robin"
	case DistributionAuto:
		return "auto"
	case DistributionWeighted:
		return "weighted"
	case DistributionSizeThreshold:
		return "size-threshold"
	}
	return "unknown"
}

// buildCLIOverrides snapshots any non-zero CLI-set DialOptions
// fields into a map[string]string for the policy script. Keys
// are the field names in snake_case; values are decimal strings.
// Called once before BeforeDial so the script sees the operator's
// inputs before the policy gets a chance to modify them.
func buildCLIOverrides(opts *DialOptions) map[string]string {
	if opts == nil {
		return nil
	}
	out := make(map[string]string, 6)
	if opts.MuxRoutes > 0 {
		out["mux_routes"] = strconv.Itoa(opts.MuxRoutes)
	}
	if opts.MinHops > 0 {
		out["min_hops"] = strconv.Itoa(opts.MinHops)
	}
	if opts.ForwardMuxRoutes > 0 {
		out["forward_mux_routes"] = strconv.Itoa(opts.ForwardMuxRoutes)
	}
	if opts.ReverseMuxRoutes > 0 {
		out["reverse_mux_routes"] = strconv.Itoa(opts.ReverseMuxRoutes)
	}
	if opts.ForwardMinHops > 0 {
		out["forward_min_hops"] = strconv.Itoa(opts.ForwardMinHops)
	}
	if opts.ReverseMinHops > 0 {
		out["reverse_min_hops"] = strconv.Itoa(opts.ReverseMinHops)
	}
	if opts.UseExistingTpOnly {
		out["use_existing_tp_only"] = "true"
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// applyAdjustment is a helper used inside DialRoutes — applies
// the hook's non-zero fields to opts. Returns ErrDialPolicyDropped
// when the hook said "drop."
func applyAdjustment(opts *DialOptions, adj DialAdjustment) error {
	if adj.Fallback == "drop" {
		return ErrDialPolicyDropped
	}
	if adj.Fallback == "direct" {
		// No-overlay path: skip the route-finder and route-setup
		// hooks; the existing-tp-only flag forces the direct
		// transport short-circuit. If no direct transport exists,
		// DialRoutes' downstream logic still fails — the script
		// gets the all-or-nothing semantic ("use direct or don't
		// dial").
		opts.UseExistingTpOnly = true
		opts.MinHops = 1
		// MuxRoutes is meaningless for a direct dial; clear it so
		// downstream code paths don't try to mux a single
		// transport.
		opts.MuxRoutes = 0
		opts.ForwardMuxRoutes = 0
		opts.ReverseMuxRoutes = 0
		return nil
	}
	if adj.MuxRoutes > 0 {
		opts.MuxRoutes = adj.MuxRoutes
	}
	if adj.ForwardMuxRoutes > 0 {
		opts.ForwardMuxRoutes = adj.ForwardMuxRoutes
	}
	if adj.ReverseMuxRoutes > 0 {
		opts.ReverseMuxRoutes = adj.ReverseMuxRoutes
	}
	if adj.MinHops > 0 {
		opts.MinHops = adj.MinHops
	}
	if adj.ForwardMinHops > 0 {
		opts.ForwardMinHops = adj.ForwardMinHops
	}
	if adj.ReverseMinHops > 0 {
		opts.ReverseMinHops = adj.ReverseMinHops
	}
	if adj.Distribution.Mode != DistributionUnset {
		opts.Distribution = adj.Distribution
	}
	return nil
}

// RouteSelectingHook is an optional secondary interface that a
// DialHook can implement to influence route selection AFTER the
// route-finder has returned its candidates. The router type-
// asserts for this interface; implementers that don't need
// candidate filtering simply omit SelectRoute and continue using
// the simpler BeforeDial path.
//
// Both hook points fire per dial:
//   - BeforeDial   (sync, before route-finder)  — adjusts opts
//   - SelectRoute  (sync, after route-finder)   — picks a route
type RouteSelectingHook interface {
	DialHook
	// SelectRoute is invoked synchronously after the route-finder
	// returns candidates and before route setup. forward and
	// reverse are the per-direction candidate lists (the same
	// path returned for both directions when the route-finder
	// query was symmetric). The hook may return Chosen / ReverseChosen
	// indices (into the respective lists) or -1 to defer per
	// direction. Drop=true refuses the dial with ErrDialPolicyDropped.
	SelectRoute(ctx context.Context, info DialInfo, forward, reverse []CandidateInfo) (RouteSelection, error)
}

// CandidateInfo is the bare-bones per-candidate description the
// router hands to RouteSelectingHook.SelectRoute. The hook is
// responsible for any further enrichment (geo lookup, transport-
// kind classification) — the router only owns the route-finder
// response and the latency lookup it already builds for its
// disjoint-path selection.
type CandidateInfo struct {
	// Hops is the ordered intermediate PKs (hex) from source
	// (excluded) to destination (excluded). Empty for a direct
	// transport.
	Hops []string

	// EstLatencyMs is the sum of per-hop latencies along the
	// route, in milliseconds, drawn from the router's existing
	// transport-tracker lookup. Zero when no measurements are
	// available — treat as "unknown," not "fast."
	EstLatencyMs int
}

// RouteSelection is the SelectRoute return value. Chosen is an
// index into the candidates slice passed in; -1 means "no
// preference, fall back to the router's built-in disjoint-path
// pick." Drop=true overrides everything and refuses the dial.
//
// Distribution lets a SelectRoute hook reconsider the per-packet
// distribution descriptor after seeing the chosen candidate's
// transport-kinds / latency / hops_geo. The hook can branch on
// the candidate it picked and return a tailored distribution
// (e.g. "if route has stcpr+sudph legs: weighted 3,1 else: auto").
// Mode == DistributionUnset means "no override at SelectRoute
// time" — falls back to whatever BeforeDial set on DialOptions.
type RouteSelection struct {
	// Chosen is the forward-direction index into the forward
	// candidates slice passed to SelectRoute, or -1 to defer.
	Chosen int
	// ReverseChosen is the reverse-direction index into the
	// reverse candidates slice, or -1 to defer (router uses its
	// built-in pickBestDirection latency rank for reverse).
	// Independent of Chosen — a script can override forward but
	// defer reverse, or vice versa.
	ReverseChosen int
	Drop          bool
	Distribution  DistributionConfig
}
