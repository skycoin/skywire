// Package preset pkg/router/policy/preset/preset.go c2-net-routing
// is the SINGLE SOURCE OF TRUTH for the built-in routing-policy
// presets' decide/tick logic, as pure Go with no wasm ABI, no
// wazero, and no starlark. It carries the preset input/output
// types as plain Go structs and the per-preset decide functions
// plus the name dispatch (Decide) and the stateful tick engine
// (Engine.OnTick).
//
// Two callers compile this same logic in and both must agree
// byte-for-byte on every decision:
//
//   - The TinyGo wasm bundle
//     (docs/examples/routing-policies/wasm/bundle/main.go) is a
//     thin JSON/pointer shim over this package; the compiled
//     bundle.wasm is what the NATIVE visor runs via wazero.
//   - The native-Go preset evaluator
//     (pkg/router/policy/preset/evaluator.go, tinygo-tagged) calls
//     this package directly so the WASM visor — which cannot host
//     wazero (wasm-in-wasm) — runs the identical preset logic.
//
// The types here intentionally mirror the flat wire structs in
// pkg/router/policy/wasm/abi.go field-for-field, but as clean Go
// structs with no json tags and no dependency on the policy
// package (which pulls in starlark and cannot compile under
// TinyGo). Callers convert their own types to/from these.
package preset

import "strings"

// Candidate mirrors policy.Candidate / CandidateWire: a concrete
// route option the policy may pick or shape.
type Candidate struct {
	Hops           []string
	HopsGeo        []string
	EstLatencyMs   int
	TransportKinds []string
}

// Context mirrors policy.RoutingContext / RoutingContextWire: the
// per-dial context a decide function reads. NowUnixNano is a
// unix-nanosecond timestamp (not time.Time) so the wasm guest and
// the native path share one integer-arithmetic clock.
type Context struct {
	App               string
	PeerPK            string
	Port              uint16
	NowUnixNano       int64
	CLIOverrides      map[string]string
	IsDirectDial      bool
	TransportKind     string
	ReverseCandidates []Candidate
}

// LegInfo mirrors policy.LegInfo / LegInfoWire: a per-leg
// telemetry snapshot the tick engine reasons over.
type LegInfo struct {
	Index       int
	Kind        string
	TransportID string
	LatencyMs   int
	Alive       bool
	Standby     bool
	SentBytes   uint64
	RecvBytes   uint64
	Retransmits uint64
	Hops        []string
}

// Spec mirrors policy.RouteSpec / RouteSpecWire: the shape a
// decide function returns. A zero Spec means "use the visor
// default."
type Spec struct {
	Chosen                  *Candidate
	ReverseChosen           *Candidate
	Mux                     int
	ForwardMux              int
	ReverseMux              int
	MinHops                 int
	ForwardMinHops          int
	ReverseMinHops          int
	Fallback                string
	Distribution            string
	RotationIntervalSeconds int
}

// RotationAction mirrors policy.RotationAction / RotationActionWire:
// the at-most-one structural change a tick returns. The zero value
// is "no-op this tick."
type RotationAction struct {
	DropLegs           []int
	AddLeg             bool
	ExcludeHops        []string
	DemoteToStandby    []int
	PromoteFromStandby []int
}

// Decide dispatches to the named preset's decide logic. The name
// is the preset selected by config ("preset:<name>"); unknown or
// empty falls back to app-mux so a bare bundle still does something
// sensible — identical to the wasm bundle's default case.
func Decide(name string, ctx Context, cands []Candidate) Spec {
	switch name {
	case "rotating-bw":
		return decideRotatingBW(ctx)
	case "latency-adaptive":
		return decideLatencyAdaptive(ctx)
	case "elastic-mux":
		return decideElasticMux(ctx)
	case "probe-and-prune":
		return decideProbeAndPrune(ctx)
	case "adaptive":
		return decideAdaptive(ctx, cands)
	case "app-mux":
		return decideAppMux(ctx)
	case "geo-avoid":
		return decideGeoAvoid(ctx, cands)
	case "transport-diverse":
		return decideTransportDiverse(ctx, cands)
	case "trust-tiered":
		return decideTrustTiered(ctx, cands)
	case "time-of-day":
		return decideTimeOfDay(ctx)
	case "ledbat":
		return decideLedbat(ctx)
	default:
		return decideAppMux(ctx)
	}
}

// decideAppMux is the verbatim app-mux preset logic: per-app static mux
// + min_hops; latency-sensitive apps stay single-route, bandwidth apps
// get parallel legs.
func decideAppMux(ctx Context) Spec {
	switch ctx.App {
	case "vpn-client":
		return Spec{Mux: 4, MinHops: 2}
	case "skychat":
		// Chat is latency-sensitive — single route, lowest mux.
		return Spec{Mux: 1}
	default:
		// Everything else: visor defaults (empty spec).
		return Spec{}
	}
}

// targetMux is the mux size the rotating-bw policy aims to maintain.
// Kept in sync with the Mux value returned from decideRotatingBW so
// on_tick can reason about "are we at target?"
const targetMux = 4

// decideRotatingBW is the rotating-bw (privacy) preset: targetMux active legs
// over multi-hop with byte load spread EQUALLY across them (traffic-analysis
// resistance — no single relay sees a large fraction of the flow), plus one
// extra warm-standby leg so on_tick can rotate the active set every 90s
// WITHOUT a tear-and-rebuild dip (see tickRotatingBW).
func decideRotatingBW(ctx Context) Spec {
	// rotating-bw is OPT-IN per app (proxy/vpn/skynet start --routing-policy, or
	// visor app arg routing-policy). Apply it to WHATEVER app/session it is
	// active for — do NOT gate on the built-in binary names. A named proxy
	// session dials under its SESSION name (e.g. "g8"), not "skysocks-client",
	// so the old `switch ctx.App { case "skysocks-client", ... }` silently made
	// the policy a NO-OP for every custom-named session (empty spec → no mux, no
	// min_hops, no rotation — the reason the rotation never fired live). Only
	// skip genuinely latency-sensitive apps, where a multi-hop mux is the wrong
	// shape.
	switch ctx.App {
	case "skychat", "skychat-client":
		return Spec{Mux: 1}
	}
	// min_hops=2 already says "no direct transport" (direct is 0 intermediates);
	// the visor treats min_hops>=2 as an implicit avoid_direct so the dial flows
	// to the overlay path where rotation can act. Without it the mux/distribution
	// is silently dropped by the direct-dial fast path.
	return Spec{
		// targetMux active + 1 warm standby (see tickRotatingBW).
		Mux:                     targetMux + 1,
		MinHops:                 2,
		RotationIntervalSeconds: 90,
		// Equal (round-robin) byte spread across the active legs — the privacy
		// property. "auto"/latency-weighting pins bytes to the fastest leg,
		// defeating the spread; the policy sets this to override the muxMode
		// default.
		Distribution: "round-robin",
	}
}

// decideLatencyAdaptive is the latency-adaptive preset's decide logic: a
// symmetric 4-wide multi-hop mux (plus one warm reserve) for bandwidth/proxy
// apps, re-evaluated every 30s so on_tick can evict the slowest leg;
// Distribution "auto" lets the host weight bytes toward the faster legs.
// Non-target apps get the empty spec (defaults).
func decideLatencyAdaptive(ctx Context) Spec {
	switch ctx.App {
	case "vpn-client", "skysocks-client", "skynet-client":
		return Spec{
			Mux:                     5,
			MinHops:                 2,
			RotationIntervalSeconds: 30,
			Distribution:            "auto",
		}
	}
	return Spec{}
}

// decideElasticMux is the elastic-mux preset's decide logic. It starts
// deliberately MODEST — a 2-way mux over multi-hop — and lets the on_tick
// AIMD controller grow or shrink the leg count to match observed load.
func decideElasticMux(ctx Context) Spec {
	switch ctx.App {
	case "vpn-client", "skysocks-client", "skynet-client":
		return Spec{
			Mux:                     2,
			MinHops:                 2,
			RotationIntervalSeconds: 20,
			Distribution:            "auto",
		}
	}
	return Spec{}
}

// decideProbeAndPrune is the probe-and-prune preset's decide logic. It holds a
// steady 3-way mux over multi-hop as the "established" set that on_tick
// continuously refines.
func decideProbeAndPrune(ctx Context) Spec {
	switch ctx.App {
	case "vpn-client", "skysocks-client", "skynet-client":
		return Spec{
			Mux:                     3,
			MinHops:                 2,
			RotationIntervalSeconds: 30,
			Distribution:            "auto",
		}
	}
	return Spec{}
}

// decideAdaptive is the COMPOSITE "adaptive" preset's decide logic — the
// intended converged default (the config generator wires "preset:adaptive").
//
// It is deliberately APP-AGNOSTIC: every overlay app — vpn-client,
// skysocks-client, skynet-client, the resolving-proxy / skynet-browser chain,
// and any future skynet consumer that dials a route group — gets the adaptive
// asymmetric mux. The one exception is latency-sensitive chat, which stays a
// single lean route (same idiom as the rotating-bw / app-mux presets). The
// behavior keys on traffic SHAPE (latency-sensitive vs. throughput), never on a
// hardcoded allowlist of built-in binary names, so a custom-named proxy/vpn
// session ("g8", …) is covered too — the reason the old three-name switch made
// the policy a silent no-op for renamed sessions.
//
// Shape — ASYMMETRIC forward/reverse with a proactively-held warm standby pool:
//   - ForwardMux=1: a single lean forward leg. The upstream / request path is
//     latency-sensitive and pays no mux head-of-line cost.
//   - ReverseMux=adaptRevActive+adaptStandbyMax: the reverse (download) leg pool
//     the router establishes up front. tickAdaptive keeps the STEADY active
//     width at adaptRevActive (=1) — so an interactive / idle flow rides ONE
//     healthy leg and is never scattered+reordered across a wide mux — and parks
//     the rest (adaptStandbyMax legs) as an always-on warm-standby reserve. The
//     reverse mux WIDENS only under SUSTAINED bulk load (promoting warm spares,
//     dip-free) and shrinks back when the load ends. tickAdaptive also keeps
//     gross-latency-outlier / dead legs OUT of the active set and rate-limits
//     reshapes (hysteresis + cooldown) so the active set stays stable under real
//     long-lived connections.
//
// No MinHops here on purpose: min-hops is a privacy constraint the operator
// owns; leaving it 0 means "inherit" so adaptive optimizes within the
// operator's chosen floor. The reverse aux legs still come out disjoint and
// multi-hop because establishMuxRoutes excludes the primary's (direct)
// transport and picks distinct-intermediate paths for the spares.
//
// The one path refinement: when real transport-kind metadata is present,
// adaptive seeds the most transport-diverse forward candidate as the initial
// route (identical selection to decideTransportDiverse — max distinct
// TransportKinds, ties broken by lower EstLatencyMs). This only ever REFINES
// which single route the mux seeds from; sizing/evict/probe/warm-standby stay
// tickAdaptive's job.
//
// Graceful degradation is load-bearing: with no candidates, or when every
// candidate has empty/unknown TransportKinds (the wasm guest / NopProvider
// path leaves Kind ""), Chosen stays nil and the router picks the path exactly
// as before — so this can never break the wasm or no-provider path.
func decideAdaptive(ctx Context, cands []Candidate) Spec {
	switch ctx.App {
	case "skychat", "skychat-client":
		// Latency-sensitive chat: single lean route, lowest mux.
		return Spec{Mux: 1}
	}
	spec := Spec{
		ForwardMux:              adaptFwdActive,
		ReverseMux:              adaptRevActive + adaptStandbyMax,
		RotationIntervalSeconds: 20,
		Distribution:            "auto",
	}
	if anyKnownTransportKind(cands) {
		spec.Chosen = mostTransportDiverse(cands)
	}
	return spec
}

// anyKnownTransportKind reports whether any candidate carries a non-empty
// transport kind — the signal that real kind metadata is available. When it is
// false (no candidates, or the wasm/NopProvider path where every kind is ""),
// diversity-based refinement is skipped so the caller keeps its default.
func anyKnownTransportKind(cands []Candidate) bool {
	for i := range cands {
		for _, k := range cands[i].TransportKinds {
			if strings.TrimSpace(k) != "" {
				return true
			}
		}
	}
	return false
}

// mostTransportDiverse returns the forward candidate whose hops span the MOST
// distinct transport kinds (ties broken by lower EstLatencyMs), or nil when no
// candidates are offered. Shared by the transport-diverse preset and the
// adaptive composite so both rank routes identically.
func mostTransportDiverse(cands []Candidate) *Candidate {
	var best *Candidate
	bestDiversity := -1
	for i := range cands {
		d := distinctCount(cands[i].TransportKinds)
		switch {
		case d > bestDiversity:
			c := cands[i]
			best, bestDiversity = &c, d
		case d == bestDiversity && best != nil && cands[i].EstLatencyMs < best.EstLatencyMs:
			c := cands[i]
			best = &c
		}
	}
	return best
}

// ledbatMux is the small symmetric mux the ledbat preset provisions and the
// hard CAP its on_tick controller may grow the active set back up to. It is
// deliberately lean: ledbat is a background / scavenger policy, so it holds few
// legs and yields (shrinks toward one) the moment it detects it is queuing.
const ledbatMux = 3

// decideLedbat is the EXPERIMENTAL ledbat preset's decide logic — a delay-based,
// background/scavenger multipath policy inspired by LEDBAT congestion control
// (RFC 6817). It provisions a small symmetric mux (ledbatMux legs) over
// multi-hop with "auto" (latency-weighted) byte distribution, re-evaluated every
// 20s so on_tick can shrink or grow the active set from the measured queuing
// delay. Latency-sensitive chat stays a single lean route (same idiom as the
// other presets). The scavenger behavior itself lives in tickLedbat: it starts
// at the small provisioned width and BACKS OFF toward a single leg whenever a
// leg's smoothed delay rises meaningfully above its own base (min) delay —
// yielding capacity to other traffic — and grows back up to ledbatMux only while
// every active leg reads near its base (no self-induced queuing).
func decideLedbat(ctx Context) Spec {
	switch ctx.App {
	case "skychat", "skychat-client":
		return Spec{Mux: 1}
	}
	return Spec{
		Mux:                     ledbatMux,
		MinHops:                 2,
		RotationIntervalSeconds: 20,
		Distribution:            "auto",
	}
}

// --- conditional presets: constrain WHICH path is chosen, by route metadata ---

// splitSet parses a comma/space-separated override value into a lowercased
// membership set. Empty input yields an empty (never-nil) set.
func splitSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if f != "" {
			out[strings.ToLower(f)] = true
		}
	}
	return out
}

// decideGeoAvoid picks the lowest-latency forward candidate whose hops transit
// NONE of the blocked countries in cli_overrides["avoid_geo"] (comma-separated
// ISO country codes). If no candidate is clean (or none are offered) it returns
// the empty spec, deferring to the router rather than forcing a violating path.
func decideGeoAvoid(ctx Context, cands []Candidate) Spec {
	blocked := splitSet(ctx.CLIOverrides["avoid_geo"])
	if len(blocked) == 0 || len(cands) == 0 {
		return Spec{} // nothing to enforce
	}
	var best *Candidate
	for i := range cands {
		if candidateTransitsBlockedGeo(cands[i], blocked) {
			continue
		}
		if best == nil || cands[i].EstLatencyMs < best.EstLatencyMs {
			c := cands[i]
			best = &c
		}
	}
	if best == nil {
		return Spec{} // every candidate violates; let the router decide
	}
	return Spec{Chosen: best, MinHops: 2}
}

// candidateTransitsBlockedGeo reports whether any of the candidate's per-hop
// countries is in the blocked set.
func candidateTransitsBlockedGeo(c Candidate, blocked map[string]bool) bool {
	for _, g := range c.HopsGeo {
		if blocked[strings.ToLower(g)] {
			return true
		}
	}
	return false
}

// decideTransportDiverse picks the forward candidate whose hops span the MOST
// distinct transport types (ties broken by lower latency). Empty spec when no
// candidates are offered.
func decideTransportDiverse(_ Context, cands []Candidate) Spec {
	best := mostTransportDiverse(cands)
	if best == nil {
		return Spec{}
	}
	// A mux of 2 over the diverse path keeps a spare leg on a different carrier.
	return Spec{Chosen: best, Mux: 2, MinHops: 2}
}

// distinctCount counts unique (case-folded) entries in a slice.
func distinctCount(xs []string) int {
	seen := map[string]bool{}
	for _, x := range xs {
		seen[strings.ToLower(x)] = true
	}
	return len(seen)
}

// decideTrustTiered prefers routes that transit ONLY trusted intermediaries
// (cli_overrides["trusted_pks"], comma-separated hop PKs). Tiered: a
// fully-trusted candidate wins outright (lowest latency among them); else it
// falls back to the candidate with the MOST trusted hops.
func decideTrustTiered(ctx Context, cands []Candidate) Spec {
	trusted := splitSet(ctx.CLIOverrides["trusted_pks"])
	if len(trusted) == 0 || len(cands) == 0 {
		return Spec{}
	}
	var bestFull, bestPartial *Candidate
	bestPartialScore := -1
	for i := range cands {
		n := trustedHopCount(cands[i], trusted)
		if len(cands[i].Hops) > 0 && n == len(cands[i].Hops) {
			if bestFull == nil || cands[i].EstLatencyMs < bestFull.EstLatencyMs {
				c := cands[i]
				bestFull = &c
			}
		}
		if n > bestPartialScore {
			c := cands[i]
			bestPartial, bestPartialScore = &c, n
		}
	}
	if bestFull != nil {
		return Spec{Chosen: bestFull, MinHops: 2}
	}
	if bestPartial != nil {
		return Spec{Chosen: bestPartial, MinHops: 2}
	}
	return Spec{}
}

// trustedHopCount counts how many of a candidate's hops are in the trusted set.
func trustedHopCount(c Candidate, trusted map[string]bool) int {
	n := 0
	for _, h := range c.Hops {
		if trusted[strings.ToLower(h)] {
			n++
		}
	}
	return n
}

// decideTimeOfDay switches the route SHAPE by wall-clock hour (UTC), derived
// from ctx.NowUnixNano. During the configured business-hours window
// (cli_overrides["business_hours"] = "START-END", default "9-17") it returns a
// lean single-route shape; outside it, a wide privacy mux with byte-spread and
// rotation.
func decideTimeOfDay(ctx Context) Spec {
	startH, endH := parseHourRange(ctx.CLIOverrides["business_hours"], 9, 17)
	if inHourRange(hourOfDayUTC(ctx.NowUnixNano), startH, endH) {
		return Spec{Mux: 1} // business hours: lean + low-latency
	}
	// off-hours: privacy-wide mux (mirrors rotating-bw's shape)
	return Spec{Mux: 4, MinHops: 2, Distribution: "round-robin", RotationIntervalSeconds: 90}
}

// hourOfDayUTC returns the UTC hour (0-23) of a unix-nanosecond timestamp using
// only integer arithmetic (avoids a "time" import in the wasm guest).
func hourOfDayUTC(unixNano int64) int {
	if unixNano <= 0 {
		return 0
	}
	secOfDay := (unixNano / 1e9) % 86400
	return int(secOfDay / 3600)
}

// parseHourRange parses "START-END" (24h) from an override, falling back to the
// given defaults on empty/malformed input.
func parseHourRange(s string, defStart, defEnd int) (int, int) {
	a, b, ok := strings.Cut(strings.TrimSpace(s), "-")
	if !ok {
		return defStart, defEnd
	}
	start, okA := atoiHour(a)
	end, okB := atoiHour(b)
	if !okA || !okB {
		return defStart, defEnd
	}
	return start, end
}

// atoiHour parses a 0-23 hour string; returns ok=false otherwise.
func atoiHour(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	if n < 0 || n > 23 {
		return 0, false
	}
	return n, true
}

// inHourRange reports whether hour is within [start, end), handling a window
// that wraps past midnight (start > end, e.g. 22-6).
func inHourRange(hour, start, end int) bool {
	if start <= end {
		return hour >= start && hour < end
	}
	return hour >= start || hour < end // wraps midnight
}

// medianSorted returns the median of an already-sorted float slice (mean of
// the two middle elements for an even count).
func medianSorted(s []float64) float64 {
	n := len(s)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// reliableMuxKind reports whether a transport type is reliable enough to ANCHOR
// a mux active set. stcpr/sudph/squicr/stcp are direct, well-behaved for
// sustained multiplexed throughput; webrtc/ws/wt/dmsg are not.
func reliableMuxKind(kind string) bool {
	switch kind {
	case "stcpr", "sudph", "squicr", "stcp":
		return true
	}
	return false
}
