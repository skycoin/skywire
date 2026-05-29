// Package policy implements operator-programmable routing policy
// for the visor's per-dial route selection. See RFC #2882
// (docs/routing_policy_rfc.md) for the design.
//
// The package's external surface:
//
//   - Evaluator: loads a Starlark policy script and exposes
//     Decide(ctx, candidates) which the router calls per dial.
//   - RoutingContext: the input passed to the script.
//   - Candidate: one possible route the script can choose from.
//   - RouteSpec: the script's structured return value.
//
// The policy is OPTIONAL — when the evaluator is nil (no policy
// configured) the router uses its built-in default selection. When
// configured, the evaluator runs the script under a timeout, with
// panics caught and translated into a fallback signal.
package policy

import (
	"time"
)

// RoutingContext is the input the visor passes to the policy
// script per dial. Fields are the union of (a) the dial-time
// inputs (app name, peer PK, target port) and (b) visor-state
// snapshots that the operator's policy might reference (current
// time, recent failure history). Expensive lookups (geoip,
// transport-tracker queries) are memoized per-visor-uptime; the
// fields populated here are the cheap ones plus the few that are
// genuinely per-dial.
type RoutingContext struct {
	// App is the registered app name making this dial — "skychat",
	// "vpn-client", etc. Policies can branch on this to apply
	// different rules per app.
	App string

	// PeerPK is the destination visor's public key (66-hex-char).
	PeerPK string

	// Port is the routing port being dialed on the remote.
	Port uint16

	// Now is the current wall-clock time at policy invocation, in
	// the operator's configured timezone. Used by datetime stdlib;
	// injectable for tests via the Evaluator's Clock.
	Now time.Time

	// CLIOverrides surfaces any per-invocation flags the operator
	// passed on the CLI (--routes N, --min-hops K, etc.). The
	// policy can choose to honor or override them. Empty map when
	// the dial came from a launched app rather than a CLI command.
	CLIOverrides map[string]string

	// IsDirectDial signals the dial is going through the
	// sky_forward_conn / VStreamMux short-circuit (an existing
	// direct transport) rather than the overlay route-finder.
	// Mux / distribution are no-ops on direct dials; the most
	// useful return from the script in this case is
	// fallback="drop" to refuse, or a bare RouteSpec() to allow.
	// Surfaced as `ctx.is_direct_dial` in Starlark.
	IsDirectDial bool

	// TransportKind is the network type ("stcpr"/"sudph"/"stcp"/
	// "dmsg") of the direct transport when IsDirectDial is true.
	// Surfaced as `ctx.transport_kind`.
	TransportKind string

	// ReverseCandidates is the reverse-direction candidate list
	// when SelectRoute is invoked, exposed to Starlark as
	// `ctx.reverse_candidates`. Empty during BeforeDial (no
	// candidates yet) and during SelectRoute when the dial used
	// a symmetric route-finder query (forward and reverse share
	// the same path candidates — same list). Scripts that want
	// per-direction filtering iterate this list separately and
	// set RouteSpec.reverse_chosen.
	ReverseCandidates []Candidate
}

// Candidate is one possible route from the visor to the peer that
// the policy can examine and choose from. The router constructs
// the candidate list before invoking the policy; the policy's job
// is to pick one (or filter / sort the list and return the head).
type Candidate struct {
	// Hops is the ordered list of intermediate visor PKs from
	// source (excluded) to destination (excluded). Empty list means
	// a direct transport.
	Hops []string

	// HopsGeo mirrors Hops with ISO-3166 country codes for each
	// intermediate, resolved via the embedded geoip database.
	// Unknown / unresolvable hops appear as "??". Policies use
	// this for geographic filtering ("only routes through ID").
	HopsGeo []string

	// EstLatencyMs is the visor's best estimate of round-trip
	// latency along this route, from the transport-tracker's
	// recent measurements. Zero when no recent measurement is
	// available — policies should treat zero as "unknown" rather
	// than "fast."
	EstLatencyMs int

	// TransportKinds is the set of transport types along the
	// route (e.g. ["stcpr"], ["dmsg"], ["stcpr", "sudph"] for
	// mixed-transport routes). Used by policies that want to
	// prefer direct-tcp paths over relayed-dmsg paths.
	TransportKinds []string
}

// RouteSpec is the structured return value from the policy
// script. The router consumes this to drive route selection +
// per-dial knob settings.
type RouteSpec struct {
	// Chosen is the forward-direction candidate the policy picked.
	// Nil means "fall back to the visor's built-in default" —
	// either because the policy returned an explicit None or
	// because every candidate was filtered out. For directional-
	// asymmetric policies, set ReverseChosen separately.
	Chosen *Candidate

	// ReverseChosen is the reverse-direction candidate, when the
	// policy wants different forward and reverse routes (e.g.
	// "stcpr upstream, sudph downstream"). Nil means "no override;
	// let the router pick the lowest-latency reverse independently."
	// The script accesses reverse candidates via ctx.reverse_candidates
	// in SelectRoute.
	ReverseChosen *Candidate

	// Mux is the symmetric number of parallel mux legs to open.
	// Zero or 1 means a single route. The router caps it at the
	// number of disjoint candidates the route-finder returned —
	// scripts that want to know that count up-front should read
	// `len(candidates)` in SelectRoute. Overridden per-direction
	// by ForwardMux / ReverseMux when those are non-zero.
	Mux int

	// ForwardMux and ReverseMux are per-direction overrides for
	// Mux. The common use case is asymmetric workloads — a
	// download-heavy app benefits from ReverseMux=N (multiple
	// download paths aggregated) and ForwardMux=1 (one cheap
	// upstream). Zero falls back to Mux.
	ForwardMux int
	ReverseMux int

	// MinHops is the symmetric minimum acceptable intermediate
	// count. Honored as a hint by the router. Overridden per-
	// direction by ForwardMinHops / ReverseMinHops when those
	// are non-zero.
	MinHops int

	// ForwardMinHops and ReverseMinHops are per-direction
	// overrides for MinHops. Useful when one direction needs more
	// hops than the other for compliance or trust reasons. Zero
	// falls back to MinHops.
	ForwardMinHops int
	ReverseMinHops int

	// Fallback names the backup strategy when Chosen is nil or
	// rejected. Recognized values:
	//   ""       — use the visor's built-in default (router's
	//              disjoint-path pick on the unfiltered candidates,
	//              or direct-transport short-circuit if one exists)
	//   "drop"   — fail the dial loudly with ErrDialPolicyDropped
	//              (operator notices; app sees a connection error)
	//   "direct" — skip the route-finder, force the direct-transport
	//              short-circuit. If no direct transport exists,
	//              the dial fails. Useful when a script's filter
	//              wipes all multihop candidates but a direct
	//              transport would still satisfy the operator
	//              (e.g. "Indonesia transit only, else direct").
	//
	// Any other value is treated as "".
	Fallback string

	// Distribution is the per-packet distribution descriptor for
	// the per-packet code path (deferred Phase 5 in the RFC).
	// Recognized values: "" (round-robin, default),
	// "weighted: a,b,c", "size-threshold:N". The per-packet path
	// stays compiled Go; this field configures it from Starlark.
	Distribution string

	// RotationIntervalSeconds, when > 0, configures the router to
	// fire the policy's on_tick hook on this route group every
	// N seconds. The hook returns a RotationAction describing
	// which legs to drop and whether to add a fresh one — the
	// mechanism behind continuous bandwidth-spreading policies
	// that rotate through the eligible-peer set. Set at dial time
	// via decide_route; cannot be changed mid-flight (drop + redial
	// to reconfigure).
	RotationIntervalSeconds int
}

// RotationAction is the structured return value from a policy's
// on_tick hook. The router fires the hook periodically per active
// route group (see RouteSpec.RotationIntervalSeconds) and applies
// this action against the group's leg set:
//
//   - DropLegs is a list of current-tick leg indices to close.
//     Indices reference the slice passed to on_tick — they are
//     NOT stable across ticks because leg-prune compacts the slice
//     when a leg closes. Policies must re-read on each tick.
//   - AddLeg requests the router to dial one more aux forward leg,
//     using ExcludeHops as the disjoint-intermediate filter. The
//     add is best-effort — if no disjoint path exists the action
//     is logged and the group keeps its current legs.
//
// Drops apply before adds, so a "drop oldest + add new" pattern
// produces one new leg replacing one old one. A no-op action
// (zero-value) leaves the group unchanged.
type RotationAction struct {
	DropLegs    []int
	AddLeg      bool
	ExcludeHops []string
}
