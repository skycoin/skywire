// Package policy pkg/router/policy/hook.go — adapter that turns
// a Loader (the operator's Starlark policy file) into a
// router.DialHook the router can invoke per dial.
//
// Living in the policy package keeps the router package free of
// the Starlark machinery; pkg/router only depends on the small
// DialHook interface in dial_hook.go.
package policy

import (
	"context"

	"github.com/skycoin/skywire/pkg/router"
)

// Hook adapts one default Loader plus optional per-app Loaders to
// the router.DialHook interface. Pass to router.Config.DialHook to
// plug a routing policy into the dial path.
//
// Per-app loaders take precedence over the default; an app with a
// configured RoutingPolicy gets its own evaluator, every other app
// falls through to the visor-wide default. An app whose per-app
// loader is registered but inactive still uses that loader (which
// returns a no-op spec) rather than escaping to the default — the
// per-app policy is the operator's intent for that app.
type Hook struct {
	loader   Engine
	byApp    map[string]Engine
	provider Provider // used for HopsGeo enrichment during SelectRoute
	logger   func(format string, args ...interface{})
}

// HookOption configures a Hook at construction.
type HookOption func(*Hook)

// WithHookProvider supplies the Provider used to enrich per-route
// candidate metadata (HopsGeo, TransportKinds) before the script's
// decide_route function sees the candidates slice. Without this the
// hook's SelectRoute still works, but every HopsGeo entry is "??"
// and TransportKinds is empty — geographic / transport-kind filters
// in the script would match nothing.
func WithHookProvider(p Provider) HookOption {
	return func(h *Hook) { h.provider = p }
}

// WithHookLogger supplies the log function the hook uses for
// non-fatal events (e.g. malformed distribution descriptor that
// can be safely ignored). Default is a no-op so silent operation
// stays the default in tests.
func WithHookLogger(f func(format string, args ...interface{})) HookOption {
	return func(h *Hook) { h.logger = f }
}

// NewHook wraps an Engine (e.g. the Starlark Loader or the WASM
// Loader from pkg/router/policy/wasm) as a DialHook. Passing nil
// is legal — the hook becomes a no-op until RegisterApp adds a
// per-app engine.
func NewHook(l Engine, opts ...HookOption) *Hook {
	h := &Hook{loader: l, logger: func(string, ...interface{}) {}}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// RegisterApp attaches a per-app Engine that overrides the default
// for dials originating from the named app. Safe to call only
// during init (before the hook is handed to the router).
func (h *Hook) RegisterApp(appName string, l Engine) {
	if h.byApp == nil {
		h.byApp = make(map[string]Engine)
	}
	h.byApp[appName] = l
}

// loaderFor returns the per-app engine if one is registered for
// info.AppName, else the default.
func (h *Hook) loaderFor(appName string) Engine {
	if h.byApp != nil {
		if l, ok := h.byApp[appName]; ok {
			return l
		}
	}
	return h.loader
}

// BeforeDial implements router.DialHook. Constructs a
// RoutingContext from the per-dial DialInfo, invokes the loader,
// projects the returned RouteSpec into a DialAdjustment the
// router can consume.
//
// The loader's failure-mode wiring (Fallback vs. Drop) determines
// what the policy script's failure looks like to the router:
//   - FailureFallback (default): a broken script returns an empty
//     RouteSpec → empty DialAdjustment → router proceeds with the
//     caller's original opts. No error surfaced.
//   - FailureDrop: errors propagate up here as a real error; the
//     router sees an error from BeforeDial and logs it but still
//     proceeds (failure-safe at the router layer too).
func (h *Hook) BeforeDial(ctx context.Context, info router.DialInfo) (router.DialAdjustment, error) {
	loader := h.loaderFor(info.AppName)
	if loader == nil || !loader.IsActive() {
		return router.DialAdjustment{}, nil
	}
	rctx := RoutingContext{
		App:           info.AppName,
		PeerPK:        info.PeerPK.Hex(),
		Port:          uint16(info.RPort),
		CLIOverrides:  info.CLIOverrides,
		IsDirectDial:  info.IsDirectDial,
		TransportKind: info.TransportKind,
	}
	spec, err := loader.Decide(ctx, rctx, nil)
	if err != nil {
		return router.DialAdjustment{}, err
	}
	adj := router.DialAdjustment{
		MuxRoutes:               spec.Mux,
		ForwardMuxRoutes:        spec.ForwardMux,
		ReverseMuxRoutes:        spec.ReverseMux,
		MinHops:                 spec.MinHops,
		ForwardMinHops:          spec.ForwardMinHops,
		ReverseMinHops:          spec.ReverseMinHops,
		Fallback:                spec.Fallback,
		RotationIntervalSeconds: spec.RotationIntervalSeconds,
	}
	// Distribution descriptor parse — failure is non-fatal
	// (script keeps the rest of the adjustment; distribution
	// falls back to the visor-wide default). Empty Distribution
	// parses to DistributionUnset, which applyAdjustment skips.
	if dist, perr := ParseDistribution(spec.Distribution); perr != nil {
		h.logger("policy %s: invalid distribution %q: %v", info.AppName, spec.Distribution, perr)
	} else {
		adj.Distribution = dist
	}
	return adj, nil
}

// SelectRoute implements router.RouteSelectingHook. Enriches the
// router's bare CandidateInfo list with HopsGeo and TransportKinds
// (via the Hook's Provider), passes the enriched candidates to the
// script's decide_route function, and translates the returned
// RouteSpec.Chosen back into an index for the router.
//
// Index translation: the script's Chosen is a *Candidate; we match
// it back to the input candidates by Hops equality. Hops is the
// stable key because it's the only field the script can't fabricate
// (provider-derived fields are read-only inside Starlark).
//
// "drop" Fallback maps to RouteSelection.Drop. An empty Chosen
// with no drop signal maps to Chosen=-1 (defer to router's pick).
func (h *Hook) SelectRoute(ctx context.Context, info router.DialInfo, forward, reverse []router.CandidateInfo) (router.RouteSelection, error) {
	loader := h.loaderFor(info.AppName)
	if loader == nil || !loader.IsActive() || len(forward) == 0 {
		return router.RouteSelection{Chosen: -1, ReverseChosen: -1}, nil
	}
	prov := h.provider
	if prov == nil {
		prov = NopProvider()
	}
	enrich := func(cs []router.CandidateInfo) []Candidate {
		out := make([]Candidate, 0, len(cs))
		for _, c := range cs {
			geoCodes := make([]string, len(c.Hops))
			kinds := make([]string, 0, len(c.Hops))
			seenKinds := make(map[string]struct{}, len(c.Hops))
			for i, pk := range c.Hops {
				geoCodes[i] = prov.Geo(pk)
				if k := prov.Kind(pk); k != "" {
					if _, ok := seenKinds[k]; !ok {
						kinds = append(kinds, k)
						seenKinds[k] = struct{}{}
					}
				}
			}
			out = append(out, Candidate{
				Hops:           append([]string(nil), c.Hops...),
				HopsGeo:        geoCodes,
				EstLatencyMs:   c.EstLatencyMs,
				TransportKinds: kinds,
			})
		}
		return out
	}
	policyCandidates := enrich(forward)
	policyReverse := enrich(reverse)
	rctx := RoutingContext{
		App:               info.AppName,
		PeerPK:            info.PeerPK.Hex(),
		Port:              uint16(info.RPort),
		CLIOverrides:      info.CLIOverrides,
		ReverseCandidates: policyReverse,
	}
	spec, err := loader.Decide(ctx, rctx, policyCandidates)
	if err != nil {
		return router.RouteSelection{Chosen: -1, ReverseChosen: -1}, err
	}
	if spec.Fallback == "drop" {
		return router.RouteSelection{Drop: true, Chosen: -1, ReverseChosen: -1}, nil
	}
	// Distribution descriptor — parsed from SelectRoute so the
	// script can branch on the chosen candidate's properties
	// (transport_kinds, hops_geo, est_latency_ms). When the
	// descriptor is empty or fails to parse, the SelectRoute-side
	// override is left unset and DialOptions.Distribution (set
	// from BeforeDial) stays in effect.
	var dist router.DistributionConfig
	if d, perr := ParseDistribution(spec.Distribution); perr != nil {
		h.logger("policy %s: invalid distribution in SelectRoute %q: %v",
			info.AppName, spec.Distribution, perr)
	} else {
		dist = d
	}
	fwdIdx := -1
	if spec.Chosen != nil {
		fwdIdx = matchCandidate(policyCandidates, *spec.Chosen)
	}
	revIdx := -1
	if spec.ReverseChosen != nil {
		revIdx = matchCandidate(policyReverse, *spec.ReverseChosen)
	}
	return router.RouteSelection{
		Chosen:        fwdIdx,
		ReverseChosen: revIdx,
		Distribution:  dist,
	}, nil
}

// matchCandidate finds the index of want in pool by Hops equality.
// Returns -1 if no match — the script returned a fabricated
// candidate that doesn't correspond to any input, in which case we
// defer to the router's built-in pick.
func matchCandidate(pool []Candidate, want Candidate) int {
	for i, c := range pool {
		if hopsEqual(c.Hops, want.Hops) {
			return i
		}
	}
	return -1
}

func hopsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// OnTick fires the policy's optional on_tick hook for periodic
// rotation. The router calls this from the per-RouteGroup
// rotation goroutine (cadence comes from
// RouteSpec.RotationIntervalSeconds set at dial time). Returns
// the zero-value action when no engine is registered, no engine
// is active, or the engine's on_tick is undefined — the router
// treats that as "no rotation this tick."
//
// Errors from the engine fall through to a zero-value action +
// the logger, matching the failure-safe pattern used elsewhere
// in the Hook. A misbehaving rotation script can't cause the
// route group to drop legs unexpectedly.
func (h *Hook) OnTick(info router.DialInfo, legs []router.LegInfo) router.RotationAction {
	loader := h.loaderFor(info.AppName)
	if loader == nil || !loader.IsActive() {
		return router.RotationAction{}
	}
	policyLegs := make([]LegInfo, 0, len(legs))
	for _, l := range legs {
		policyLegs = append(policyLegs, LegInfo{
			Index:     l.Index,
			Kind:      l.Kind,
			LatencyMs: l.LatencyMs,
			Alive:     l.Alive,
		})
	}
	rctx := RoutingContext{
		App:    info.AppName,
		PeerPK: info.PeerPK.Hex(),
		Port:   uint16(info.RPort),
	}
	action, err := loader.OnTick(context.Background(), rctx, policyLegs)
	if err != nil {
		h.logger("policy %s: OnTick error: %v", info.AppName, err)
		return router.RotationAction{}
	}
	return router.RotationAction{
		DropLegs:    append([]int(nil), action.DropLegs...),
		AddLeg:      action.AddLeg,
		ExcludeHops: append([]string(nil), action.ExcludeHops...),
	}
}

// OnLegChange implements router.LegChangeHook. Called by the
// route group whenever its leg set mutates. Translates the
// router-side LegInfo / LegChange to the policy-side equivalents,
// invokes the script's optional on_leg_change function, and
// projects any returned distribution descriptor into a
// DistributionConfig for the route group to apply.
//
// Returns the zero DistributionConfig (Mode == DistributionUnset)
// when the script doesn't define on_leg_change, when the loader
// is inactive, or when the parse fails — the route group treats
// that as "leave distribution unchanged."
func (h *Hook) OnLegChange(info router.DialInfo, legs []router.LegInfo, change router.LegChange) router.DistributionConfig {
	loader := h.loaderFor(info.AppName)
	if loader == nil || !loader.IsActive() {
		return router.DistributionConfig{}
	}
	policyLegs := make([]LegInfo, 0, len(legs))
	for _, l := range legs {
		policyLegs = append(policyLegs, LegInfo{
			Index:     l.Index,
			Kind:      l.Kind,
			LatencyMs: l.LatencyMs,
			Alive:     l.Alive,
		})
	}
	policyChange := LegChange{Event: change.Event, LegIndex: change.LegIndex}
	rctx := RoutingContext{
		App:    info.AppName,
		PeerPK: info.PeerPK.Hex(),
		Port:   uint16(info.RPort),
	}
	spec, err := loader.OnLegChange(context.Background(), rctx, policyLegs, policyChange)
	if err != nil {
		h.logger("policy %s: OnLegChange error: %v", info.AppName, err)
		return router.DistributionConfig{}
	}
	dist, perr := ParseDistribution(spec.Distribution)
	if perr != nil {
		h.logger("policy %s: OnLegChange returned invalid distribution %q: %v",
			info.AppName, spec.Distribution, perr)
		return router.DistributionConfig{}
	}
	return dist
}
