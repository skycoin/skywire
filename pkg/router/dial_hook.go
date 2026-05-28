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
}

// DialAdjustment is the hook's return value. Each field is a
// "override this if non-zero" hint; zero values mean "no change
// for this knob." Fallback can be set to "drop" to refuse the
// dial entirely.
type DialAdjustment struct {
	// MuxRoutes, when > 0, overrides DialOptions.MuxRoutes for
	// this dial.
	MuxRoutes int

	// MinHops, when > 0, overrides DialOptions.MinHops for this
	// dial.
	MinHops int

	// Fallback, when "drop", causes DialRoutes to refuse the
	// dial with ErrDialPolicyDropped. Any other value is
	// ignored (the dial proceeds normally).
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

// applyAdjustment is a helper used inside DialRoutes — applies
// the hook's non-zero fields to opts. Returns ErrDialPolicyDropped
// when the hook said "drop."
func applyAdjustment(opts *DialOptions, adj DialAdjustment) error {
	if adj.Fallback == "drop" {
		return ErrDialPolicyDropped
	}
	if adj.MuxRoutes > 0 {
		opts.MuxRoutes = adj.MuxRoutes
	}
	if adj.MinHops > 0 {
		opts.MinHops = adj.MinHops
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
	// returns candidates and before route setup. The hook may
	// return a Chosen index (into the candidates slice) or -1 to
	// defer to the router's built-in selection. Drop=true refuses
	// the dial with ErrDialPolicyDropped.
	SelectRoute(ctx context.Context, info DialInfo, candidates []CandidateInfo) (RouteSelection, error)
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
type RouteSelection struct {
	Chosen int
	Drop   bool
}
