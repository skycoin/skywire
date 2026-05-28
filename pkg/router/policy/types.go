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

import "time"

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

	// MuxLegsAvailable indicates how many parallel mux legs can
	// be opened along this route. Bounded by the route's
	// intermediates' capabilities. Zero means "single leg only."
	MuxLegsAvailable int
}

// RouteSpec is the structured return value from the policy
// script. The router consumes this to drive route selection +
// per-dial knob settings.
type RouteSpec struct {
	// Chosen is the candidate the policy picked. Nil means "fall
	// back to the visor's built-in default" — either because the
	// policy returned an explicit None or because every candidate
	// was filtered out.
	Chosen *Candidate

	// Mux is the number of parallel mux legs to open. Zero or 1
	// means a single route. Capped by Chosen.MuxLegsAvailable.
	Mux int

	// MinHops is the minimum acceptable intermediate count.
	// Honored as a hint by the router; if Chosen already satisfies
	// it, no change. If Chosen doesn't, the router rejects Chosen
	// and falls back per Fallback.
	MinHops int

	// Fallback names the backup strategy when Chosen is nil or
	// rejected. Recognized values:
	//   ""       — use the visor's built-in default
	//   "direct" — try a direct transport, no overlay
	//   "drop"   — fail the dial loudly (operator notices)
	Fallback string

	// Distribution is the per-packet distribution descriptor for
	// the per-packet code path (deferred Phase 5 in the RFC).
	// Recognized values: "" (round-robin, default),
	// "weighted: a,b,c", "size-threshold:N". The per-packet path
	// stays compiled Go; this field configures it from Starlark.
	Distribution string
}
