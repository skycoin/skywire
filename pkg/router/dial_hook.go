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
}

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
