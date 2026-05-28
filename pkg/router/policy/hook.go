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
	loader   *Loader
	byApp    map[string]*Loader
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

// NewHook wraps a Loader as a DialHook.
func NewHook(l *Loader, opts ...HookOption) *Hook {
	h := &Hook{loader: l, logger: func(string, ...interface{}) {}}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// RegisterApp attaches a per-app Loader that overrides the default
// for dials originating from the named app. Safe to call only
// during init (before the hook is handed to the router).
func (h *Hook) RegisterApp(appName string, l *Loader) {
	if h.byApp == nil {
		h.byApp = make(map[string]*Loader)
	}
	h.byApp[appName] = l
}

// loaderFor returns the per-app loader if one is registered for
// info.AppName, else the default.
func (h *Hook) loaderFor(appName string) *Loader {
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
		App:    info.AppName,
		PeerPK: info.PeerPK.Hex(),
		Port:   uint16(info.RPort),
	}
	spec, err := loader.Decide(ctx, rctx, nil)
	if err != nil {
		return router.DialAdjustment{}, err
	}
	adj := router.DialAdjustment{
		MuxRoutes: spec.Mux,
		MinHops:   spec.MinHops,
		Fallback:  spec.Fallback,
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
func (h *Hook) SelectRoute(ctx context.Context, info router.DialInfo, candidates []router.CandidateInfo) (router.RouteSelection, error) {
	loader := h.loaderFor(info.AppName)
	if loader == nil || !loader.IsActive() || len(candidates) == 0 {
		return router.RouteSelection{Chosen: -1}, nil
	}
	prov := h.provider
	if prov == nil {
		prov = NopProvider()
	}
	policyCandidates := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
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
		policyCandidates = append(policyCandidates, Candidate{
			Hops:           append([]string(nil), c.Hops...),
			HopsGeo:        geoCodes,
			EstLatencyMs:   c.EstLatencyMs,
			TransportKinds: kinds,
		})
	}
	rctx := RoutingContext{
		App:    info.AppName,
		PeerPK: info.PeerPK.Hex(),
		Port:   uint16(info.RPort),
	}
	spec, err := loader.Decide(ctx, rctx, policyCandidates)
	if err != nil {
		return router.RouteSelection{Chosen: -1}, err
	}
	if spec.Fallback == "drop" {
		return router.RouteSelection{Drop: true, Chosen: -1}, nil
	}
	if spec.Chosen == nil {
		return router.RouteSelection{Chosen: -1}, nil
	}
	idx := matchCandidate(policyCandidates, *spec.Chosen)
	return router.RouteSelection{Chosen: idx}, nil
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
