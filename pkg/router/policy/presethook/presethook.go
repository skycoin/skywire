// Package presethook pkg/router/policy/presethook/presethook.go c2-net-routing
// adapts a built-in routing-policy preset (pkg/router/policy/preset) to the
// router.DialHook / RouteSelectingHook / RotationHook interfaces WITHOUT the
// starlark evaluator or the wazero runtime.
//
// The native visor runs presets by loading the embedded bundle.wasm and
// evaluating it in wazero (pkg/router/policy/wasm/presets, wired through
// pkg/visor/policy_loader.go + pkg/router/policy.Hook). That path pulls in
// starlark and a host wasm runtime, neither of which compiles for the browser /
// TinyGo wasm-visor — and a wasm module cannot host wazero (wasm-in-wasm)
// anyway. This package is the wasm-visor's equivalent: it calls the SAME preset
// decide/tick logic (compiled in as native Go, the single source of truth) and
// projects the result onto the same router hook interfaces the policy.Hook
// implements, so the router integration point is identical.
//
// It imports only pkg/router (for the hook interface types, already compiled
// into the wasm-visor) and pkg/router/policy/preset (stdlib-only), so it is
// TinyGo-safe. The native visor keeps using the wazero path; this is selected
// on the wasm side (see cmd/wasm-visor).
package presethook

import (
	"context"

	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/policy/preset"
)

// Provider supplies per-hop metadata the conditional presets read during route
// selection: the intermediary's country (geo-avoid) and transport kind
// (transport-diverse). The wasm-visor may pass nil (→ NopProvider), in which
// case geo/kind are unknown and those two presets defer rather than constrain;
// trust-tiered needs only the hop PKs and works regardless. Mirrors the
// enrichment pkg/router/policy.Hook does via its policy.Provider on native.
type Provider interface {
	Geo(pk string) string  // ISO country code, or "??" if unknown
	Kind(pk string) string // transport kind ("stcpr"/…), or "" if unknown
}

// NopProvider returns geo "??" and empty kind for every PK — the safe default
// when the wasm-visor has no metadata source wired.
func NopProvider() Provider { return nopProvider{} }

type nopProvider struct{}

func (nopProvider) Geo(string) string  { return "??" }
func (nopProvider) Kind(string) string { return "" }

// Hook adapts one preset to the router hook interfaces. Construct with New. It
// carries a preset.Engine so the stateful tick controllers (rotating-bw,
// latency-adaptive, elastic-mux, probe-and-prune, adaptive) accumulate their
// per-transport_id state across ticks exactly as the wazero module does.
//
// Not safe for concurrent Decide/OnTick across route groups sharing one Hook —
// construct one Hook per policy scope, as the router does per route group.
type Hook struct {
	name     string
	engine   *preset.Engine
	provider Provider
}

// New returns a Hook bound to the named preset. An unknown name still decides
// via the preset package's app-mux fallback (matching the bundle); callers that
// want to reject typos check preset.Has first. provider may be nil.
func New(name string, provider Provider) *Hook {
	if provider == nil {
		provider = NopProvider()
	}
	return &Hook{name: name, engine: preset.New(), provider: provider}
}

// Name returns the preset this hook runs.
func (h *Hook) Name() string { return h.name }

// BeforeDial implements router.DialHook: it maps the preset's decide-time route
// SHAPE (mux / min_hops / distribution / rotation) onto a DialAdjustment. The
// candidate-picking presets (geo-avoid etc.) return no shape here and act in
// SelectRoute instead.
func (h *Hook) BeforeDial(_ context.Context, info router.DialInfo) (router.DialAdjustment, error) {
	spec := preset.Decide(h.name, dialInfoToCtx(info), nil)
	// AvoidDirect = "wants a route-group overlay, not the bare direct-dial
	// short-circuit." Two reasons, matching policy.Hook: (1) min_hops >= 2 (any
	// direction) can't be met by a 0-intermediate direct hop; (2) a mux overlay
	// (Mux/ForwardMux/ReverseMux > 1) needs the route-group path to build its aux
	// legs. min_hops is a FLOOR, not a ceiling: at min_hops=1 the mux overlay
	// still applies — the direct route becomes the group's 1-hop forward leg
	// (floor) with mux legs layered on top; it must never DISABLE the mux.
	avoidDirect := spec.MinHops >= 2 || spec.ForwardMinHops >= 2 || spec.ReverseMinHops >= 2 ||
		spec.Mux > 1 || spec.ForwardMux > 1 || spec.ReverseMux > 1
	return router.DialAdjustment{
		MuxRoutes:               spec.Mux,
		ForwardMuxRoutes:        spec.ForwardMux,
		ReverseMuxRoutes:        spec.ReverseMux,
		MinHops:                 spec.MinHops,
		ForwardMinHops:          spec.ForwardMinHops,
		ReverseMinHops:          spec.ReverseMinHops,
		Fallback:                spec.Fallback,
		RotationIntervalSeconds: spec.RotationIntervalSeconds,
		AvoidDirect:             avoidDirect,
		Distribution:            distributionFor(spec.Distribution),
	}, nil
}

// SelectRoute implements router.RouteSelectingHook: it enriches the router's
// bare candidates with per-hop geo/kind (via the Provider), runs the preset's
// decide over them, and translates the chosen candidate back to an index by hop
// equality — the same protocol policy.Hook uses. Presets that return no Chosen
// defer (index -1) to the router's built-in pick.
func (h *Hook) SelectRoute(_ context.Context, info router.DialInfo, forward, reverse []router.CandidateInfo) (router.RouteSelection, error) {
	if len(forward) == 0 {
		return router.RouteSelection{Chosen: -1, ReverseChosen: -1}, nil
	}
	fwd := h.enrich(forward)
	rev := h.enrich(reverse)
	ctx := dialInfoToCtx(info)
	ctx.ReverseCandidates = rev
	spec := preset.Decide(h.name, ctx, fwd)
	if spec.Fallback == "drop" {
		return router.RouteSelection{Drop: true, Chosen: -1, ReverseChosen: -1}, nil
	}
	sel := router.RouteSelection{Chosen: -1, ReverseChosen: -1, Distribution: distributionFor(spec.Distribution)}
	if spec.Chosen != nil {
		sel.Chosen = matchCandidate(fwd, *spec.Chosen)
	}
	if spec.ReverseChosen != nil {
		sel.ReverseChosen = matchCandidate(rev, *spec.ReverseChosen)
	}
	return sel, nil
}

// OnTick implements router.RotationHook: it runs the preset's stateful tick
// controller over the current leg snapshot and returns the structural action
// (promote/demote/add/drop) for the route group to apply.
func (h *Hook) OnTick(_ router.DialInfo, legs []router.LegInfo) router.RotationAction {
	action := h.engine.OnTick(h.name, legsToPreset(legs))
	return router.RotationAction{
		DropLegs:           action.DropLegs,
		AddLeg:             action.AddLeg,
		ExcludeHops:        action.ExcludeHops,
		DemoteToStandby:    action.DemoteToStandby,
		PromoteFromStandby: action.PromoteFromStandby,
		AddForwardLeg:      action.AddForwardLeg,
	}
}

// enrich converts router CandidateInfo to preset.Candidate, filling per-hop geo
// codes and the distinct transport kinds along the path from the Provider.
func (h *Hook) enrich(cs []router.CandidateInfo) []preset.Candidate {
	if len(cs) == 0 {
		return nil
	}
	out := make([]preset.Candidate, 0, len(cs))
	for _, c := range cs {
		geo := make([]string, len(c.Hops))
		var kinds []string
		seen := map[string]struct{}{}
		for i, pk := range c.Hops {
			geo[i] = h.provider.Geo(pk)
			if k := h.provider.Kind(pk); k != "" {
				if _, ok := seen[k]; !ok {
					seen[k] = struct{}{}
					kinds = append(kinds, k)
				}
			}
		}
		out = append(out, preset.Candidate{
			Hops:           append([]string(nil), c.Hops...),
			HopsGeo:        geo,
			EstLatencyMs:   c.EstLatencyMs,
			TransportKinds: kinds,
		})
	}
	return out
}

func dialInfoToCtx(info router.DialInfo) preset.Context {
	return preset.Context{
		App:           info.AppName,
		PeerPK:        info.PeerPK.Hex(),
		Port:          uint16(info.RPort),
		CLIOverrides:  info.CLIOverrides,
		IsDirectDial:  info.IsDirectDial,
		TransportKind: info.TransportKind,
	}
}

func legsToPreset(legs []router.LegInfo) []preset.LegInfo {
	if len(legs) == 0 {
		return nil
	}
	out := make([]preset.LegInfo, len(legs))
	for i, l := range legs {
		out[i] = preset.LegInfo{
			Index:       l.Index,
			Kind:        l.Kind,
			TransportID: l.TransportID,
			LatencyMs:   l.LatencyMs,
			Alive:       l.Alive,
			Standby:     l.Standby,
			SentBytes:   l.SentBytes,
			RecvBytes:   l.RecvBytes,
			Retransmits: l.Retransmits,
			Hops:        l.Hops,
		}
	}
	return out
}

// matchCandidate finds want in pool by hop equality (the stable, un-fabricable
// key), returning -1 when no candidate matches so the router keeps its own pick.
func matchCandidate(pool []preset.Candidate, want preset.Candidate) int {
	for i := range pool {
		if hopsEqual(pool[i].Hops, want.Hops) {
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

// distributionFor maps the descriptor strings the presets emit ("round-robin",
// "auto", "") onto a router.DistributionConfig. The presets only ever emit
// these; any other value maps to Unset (defer to the visor-wide muxMode). This
// is the minimal parse the wasm path needs — the native path's full descriptor
// grammar (pkg/router/policy.ParseDistribution) lives in the starlark-tainted
// policy package and isn't needed here.
func distributionFor(desc string) router.DistributionConfig {
	switch desc {
	case "round-robin":
		return router.DistributionConfig{Mode: router.DistributionRoundRobin}
	case "auto":
		return router.DistributionConfig{Mode: router.DistributionAuto}
	case "capacity":
		return router.DistributionConfig{Mode: router.DistributionCapacity}
	default:
		return router.DistributionConfig{Mode: router.DistributionUnset}
	}
}
