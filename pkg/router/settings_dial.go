// Package router pkg/router/settings_dial.go c2-net-routing
//
// The DIAL-TIME half of the router's knob bridge: the route-ranking priors,
// how many candidates a dial asks the finder for, the warm-plan cache shape,
// the dead-route young-death window, the dial-time tunnel width and the
// operator's prefer-these-peers list.
//
// Same contract as settings.go, and the SAME catalog: every scalar here is a
// pkg/router/routersettings entry, so it is set with `route settings k=v`, read
// back in `route settings --json` under knobs / knob_detail, scoped per app
// where a dial knows the app, persisted into routing.router_settings and
// restored at the next start. `route settings dial` is a convenience VIEW over
// this section of the catalog, not a second mechanism.
//
// It is a separate file from settings.go only to keep the two sets reviewable
// apart: settings.go holds the send-window/bottleneck/forward knobs that act on
// a LIVE route group, this one holds the knobs a dial reads while it is picking
// a route.
//
// The one non-catalog member is the prefer-these-peers LIST: the catalog's
// payload is an int64 per knob, which a set of public keys is not. It keeps its
// own atomic here and its own RPC field.
package router

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
)

// Compiled defaults for the dial-time knobs. Each is the constant the ranking
// used before it became settable, and TestCatalogDefaultsMatchConstants asserts
// each against its catalog entry.
const (
	// dialUnknownLatencyCostDefaultMs is pathLatencyScore's penalty for a hop
	// with no latency measurement at all — set above the practical upper bound
	// of healthy single-hop latency so one unknown hop already loses to a known
	// 500ms hop, while an all-unknown path still beats no path.
	dialUnknownLatencyCostDefaultMs = 1000.0

	// dialUnknownHopPenaltyDefaultMs is what one unmeasured hop costs a
	// PARTIALLY measured path in pathLatencyPartialMs (see the const comment
	// there: above a typical intra-continent hop, below a bad one).
	dialUnknownHopPenaltyDefaultMs = 150.0

	// dialPriorScaleDefault leaves the transport-type and measured-throughput
	// priors at the magnitudes transportTypeCostMs / transportCostMs document.
	// A scale of 0 disables that term entirely (rank on RTT alone); >1 makes
	// the ranker more opinionated about carrier class.
	dialPriorScaleDefault = 1.0

	// dialTunnelLegsDefault is 0 — "dial exactly the leg count the app asked
	// for", today's behavior. See DialTunnelLegs.
	dialTunnelLegsDefault = 0
)

// dialPreferPKsV holds the operator's prefer-these-peers list. A candidate
// route that traverses one of these PKs ranks ahead of one that does not,
// ABOVE carrier class and latency — it is the operator saying "these are the
// routes that won, use them". Empty (the default) leaves the ranking exactly as
// it was. Not a catalog knob: the catalog's payload is one int64 per entry.
//
//nolint:gochecknoglobals // package state by design, like the catalog itself
var dialPreferPKsV atomic.Pointer[[]cipher.PubKey]

// ---------------------------------------------------------------------------
// The ranking priors.

// DialUnknownLatencyCostMs is the per-hop cost of a hop with no latency
// measurement, in milliseconds.
func DialUnknownLatencyCostMs() float64 { return routersettings.DialUnknownLatencyCostMs.Ratio() }

// SetDialUnknownLatencyCostMs installs that cost. Negative / NaN / Inf refused;
// 0 drops the penalty.
func SetDialUnknownLatencyCostMs(v float64) bool {
	return setRatio(routersettings.DialUnknownLatencyCostMs, v)
}

// DialUnknownHopPenaltyMs is what one unmeasured hop costs a partially measured
// path, in milliseconds.
func DialUnknownHopPenaltyMs() float64 { return routersettings.DialUnknownHopPenaltyMs.Ratio() }

// SetDialUnknownHopPenaltyMs installs that penalty. Negative / NaN / Inf
// refused; 0 drops the penalty.
func SetDialUnknownHopPenaltyMs(v float64) bool {
	return setRatio(routersettings.DialUnknownHopPenaltyMs, v)
}

// DialTypePriorScale multiplies the transport-TYPE prior (transportTypeCostMs),
// the penalty a hop carries until its link has a throughput measurement.
func DialTypePriorScale() float64 { return routersettings.DialTypePriorScale.Ratio() }

// SetDialTypePriorScale installs that multiplier; 0 removes the term.
func SetDialTypePriorScale(v float64) bool {
	return setRatio(routersettings.DialTypePriorScale, v)
}

// DialThroughputPriorScale multiplies the MEASURED-throughput band penalty in
// transportCostMs.
func DialThroughputPriorScale() float64 { return routersettings.DialThroughputPriorScale.Ratio() }

// SetDialThroughputPriorScale installs that multiplier; 0 removes the term.
func SetDialThroughputPriorScale(v float64) bool {
	return setRatio(routersettings.DialThroughputPriorScale, v)
}

// ---------------------------------------------------------------------------
// Candidate supply.

// DialRouteCandidates is the floor on how many routes a mux dial asks the
// route-finder for (the historical default route count).
func DialRouteCandidates() int { return routersettings.DialCandidates.Int() }

// SetDialRouteCandidates installs that floor. Non-positive is refused.
func SetDialRouteCandidates(n int) bool { return setInt(routersettings.DialCandidates, int64(n)) }

// DialMuxRouteHeadroom is the extra routes requested on top of the mux degree,
// so the disjoint-path pick can still reach the target count when some returned
// candidates share intermediates — the sibling-diversify count.
func DialMuxRouteHeadroom() int { return routersettings.DialCandidateHeadroom.Int() }

// SetDialMuxRouteHeadroom installs that headroom. Negative is refused; 0 means
// "ask for exactly the mux degree".
func SetDialMuxRouteHeadroom(n int) bool {
	return setInt(routersettings.DialCandidateHeadroom, int64(n))
}

// dialDiversifyCandidatesDefault is that window's width. It has to exceed the
// depth the pool can reach, because the finder answers rank-ordered: a window
// of 20 against a client holding 750 transports to intermediates meant the free
// first hops sat just outside it and the pool reported itself out of disjoint
// paths while most of the topology was untried. 128 covers a pool far deeper
// than any measured one; the cost is a longer candidate list to rank, which is
// a local sort.
const dialDiversifyCandidatesDefault = 128

// DialDiversifyCandidates is how many routes a DIVERSIFY dial asks the route
// finder for. A standby-pool fill is not looking for the best route, it is
// looking for one the tunnels it already holds do not use — and the finder
// answers rank-ordered, so the top few are exactly the ones they do.
func DialDiversifyCandidates() int { return routersettings.DialDiversifyCandidates.Int() }

// SetDialDiversifyCandidates installs that count. Non-positive is refused.
func SetDialDiversifyCandidates(n int) bool {
	return setInt(routersettings.DialDiversifyCandidates, int64(n))
}

// DialForegroundMux bounds how many mux legs are established SYNCHRONOUSLY at
// dial time; the background self-heal fills the rest of the pool.
func DialForegroundMux() int { return routersettings.DialForegroundMux.Int() }

// SetDialForegroundMux installs that bound. Non-positive is refused.
func SetDialForegroundMux(n int) bool { return setInt(routersettings.DialForegroundMux, int64(n)) }

// ---------------------------------------------------------------------------
// Dial-time tunnel width.

// DialTunnelLegs is the leg count an APP TUNNEL is dialed with — the fix for a
// tunnel that asks for a single-route group (MuxRoutes=1) and then has its legs
// bolted on afterwards by `proxy mux add` / the background self-heal.
//
//	 0 (default) — dial exactly what the app asked for. Today's behavior.
//	-1           — follow the visor's mux width (preset.AdaptRevActive), so a
//	               tunnel is dialed with the legs the adaptive engine would have
//	               grown it to anyway.
//	 n > 1       — dial every tunnel with n legs.
//
// Only a dial that asked for a single-route group is raised; a dial that named
// its own mux degree, a --direct dial and a datagram dial are all untouched.
//
// This is the visor-wide value; dialTunnelLegsFor reads the same knob through
// an app's overrides, which is what `route settings --app <name>
// dial.tunnel_legs=N` sets.
func DialTunnelLegs() int { return routersettings.DialTunnelLegs.Int() }

// dialTunnelLegsFor is DialTunnelLegs resolved for one app — a dial is the one
// place in the dial-time set that knows whose tunnel it is (opts.AppName), so
// a subject client and its paired reference can be dialed at different widths
// on one visor.
func dialTunnelLegsFor(app string) int {
	if app == "" {
		return DialTunnelLegs()
	}
	return routersettings.Resolve(app).Int(routersettings.DialTunnelLegs)
}

// SetDialTunnelLegs installs that count. Values below -1 are refused.
func SetDialTunnelLegs(n int) bool { return setInt(routersettings.DialTunnelLegs, int64(n)) }

// muxAppWidthFor resolves the mux.app_width knob for one app: its "app=n"
// entry in the list, if any. ok is false when app has no entry, or the entry
// does not parse to a positive count — a malformed or missing entry falls
// through to the caller's own default rather than widening/narrowing anything.
func muxAppWidthFor(app string) (int, bool) {
	if app == "" {
		return 0, false
	}
	for _, tok := range routersettings.MuxAppWidth.Strings() {
		name, val, found := strings.Cut(tok, "=")
		if !found || name != app {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil || n <= 0 {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Dead-route evidence and the warm plan cache.

// DeadRouteYoungAge is the "died right after dial" window: a group that lives
// longer than this and then closes is a normal teardown, not a dead route.
func DeadRouteYoungAge() time.Duration { return routersettings.DeadRouteYoungAge.Duration() }

// SetDeadRouteYoungAge installs that window. Non-positive is refused.
func SetDeadRouteYoungAge(d time.Duration) bool {
	return setInt(routersettings.DeadRouteYoungAge, int64(d))
}

// WarmPlanTTL bounds staleness of a cached disjoint plan set in the warm route
// pool.
func WarmPlanTTL() time.Duration { return routersettings.WarmPlanTTL.Duration() }

// SetWarmPlanTTL installs that TTL. Non-positive is refused.
func SetWarmPlanTTL(d time.Duration) bool { return setInt(routersettings.WarmPlanTTL, int64(d)) }

// WarmPlanBucketCap bounds how many distinct disjoint plans the warm pool holds
// per exit.
func WarmPlanBucketCap() int { return routersettings.WarmPlanBucketCap.Int() }

// SetWarmPlanBucketCap installs that cap. Non-positive is refused.
func SetWarmPlanBucketCap(n int) bool {
	return setInt(routersettings.WarmPlanBucketCap, int64(n))
}

// ---------------------------------------------------------------------------
// The prefer-these-peers list.

// DialPreferPKs returns the operator's prefer-these-peers list — the routes
// that should be taken FIRST. Nil when unset (the ranking is untouched).
func DialPreferPKs() []cipher.PubKey {
	p := dialPreferPKsV.Load()
	if p == nil {
		return nil
	}
	return *p
}

// SetDialPreferPKs installs the prefer list. An empty or nil slice clears it.
// A copy is kept so the caller may reuse its slice.
func SetDialPreferPKs(pks []cipher.PubKey) {
	if len(pks) == 0 {
		dialPreferPKsV.Store(nil)
		return
	}
	cp := append([]cipher.PubKey(nil), pks...)
	dialPreferPKsV.Store(&cp)
}

// pathPrefersPK reports whether path touches one of the operator's preferred
// peers — matched on every hop's destination EXCEPT the last (the exit itself
// is on every candidate and so separates nothing), so a preferred first-hop
// peer and a preferred intermediate both count. Always false when the list is
// empty, which is the default: the ranking is then exactly what it was.
func pathPrefersPK(path []routing.Hop) bool {
	want := DialPreferPKs()
	if len(want) == 0 || len(path) == 0 {
		return false
	}
	for i, h := range path {
		if i == len(path)-1 {
			break
		}
		for _, pk := range want {
			if h.To == pk {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Batched route setup.

// Compiled defaults for the batched-setup knobs. TestCatalogDefaultsMatchConstants
// asserts each against its catalog entry.
const (
	// setupBatchWindowDefault is how long a route setup waits for siblings to
	// the same exit before sending. It is short on purpose: the window is paid
	// by the FIRST dial of a burst, and a standby-pool fill or a --tunnels N
	// dial launches its siblings within a few milliseconds of each other, so
	// 40 ms collects the burst while adding nothing measurable to a lone dial
	// (which would otherwise be waiting on a p50 0.6-2.1 s setup round trip).
	setupBatchWindowDefault = 40 * time.Millisecond

	// setupBatchMaxDefault is the most routes one batched request carries. Half
	// of routing.MaxBatchRoutes, so raising the pool size does not by itself
	// push a single request to the protocol limit.
	setupBatchMaxDefault = 16

	// setupFillInflightDefault is how many standby-pool dials run at once. It
	// is also the batch size a fill can offer: the pool used to dial ONE tunnel
	// per ~7 s tick, which gave the coalescer nothing to collect.
	setupFillInflightDefault = 8

	// setupPlanClaimTTLDefault is how long one concurrent dial holds its claim
	// on an oracle candidate path. Long enough to cover a slow setup (max 7.8 s
	// observed) with headroom, short enough that a dial which died without
	// releasing frees its intermediate well inside a fill round.
	setupPlanClaimTTLDefault = 20 * time.Second
)

// SetupBatchWindow is how long a route setup collects sibling dials to the same
// exit before sending them as one batched request.
func SetupBatchWindow() time.Duration { return routersettings.SetupBatchWindow.Duration() }

// SetSetupBatchWindow installs that window. Non-positive is refused.
func SetSetupBatchWindow(d time.Duration) bool {
	return setInt(routersettings.SetupBatchWindow, int64(d))
}

// SetupBatchMax is the most routes one batched setup request carries. 1 means
// "send singles" — the batch form is never used.
func SetupBatchMax() int { return routersettings.SetupBatchMax.Int() }

// SetSetupBatchMax installs that cap. Non-positive is refused; values above
// routing.MaxBatchRoutes are clamped by the sender.
func SetSetupBatchMax(n int) bool { return setInt(routersettings.SetupBatchMax, int64(n)) }

// SetupFillInflight is how many standby-pool tunnel dials run concurrently.
func SetupFillInflight() int { return routersettings.SetupFillInflight.Int() }

// SetSetupFillInflight installs that bound. Non-positive is refused.
func SetSetupFillInflight(n int) bool { return setInt(routersettings.SetupFillInflight, int64(n)) }

// SetupPlanClaimTTL is how long a concurrent dial holds its claim on an oracle
// candidate path, so N dials in one fill take N distinct intermediates.
func SetupPlanClaimTTL() time.Duration { return routersettings.SetupPlanClaimTTL.Duration() }

// SetSetupPlanClaimTTL installs that TTL. Non-positive is refused.
func SetSetupPlanClaimTTL(d time.Duration) bool {
	return setInt(routersettings.SetupPlanClaimTTL, int64(d))
}

// setupFirstHopFilterMaxDefault is how many held first hops a dial may exclude
// as a HARD filter before first-hop diversity becomes a ranking term instead —
// which by default never happens: the threshold is consulted only when
// pool.allow_duplicate_route has opted in to sharing a first hop. Without that
// opt-in a pool with no free first hop settles, because the answer to "every
// candidate's first hop is taken" is a wider window
// (dial.diversify_candidates), not a second tunnel on a held transport.
// See freeFirstHops.
const setupFirstHopFilterMaxDefault = 8

// SetupFirstHopFilterMax is the held-first-hop count beyond which first-hop
// diversity is a ranking term rather than a filter — under
// pool.allow_duplicate_route only.
func SetupFirstHopFilterMax() int { return routersettings.SetupFirstHopFilterMax.Int() }

// SetSetupFirstHopFilterMax installs that threshold. Non-positive is refused.
func SetSetupFirstHopFilterMax(n int) bool {
	return setInt(routersettings.SetupFirstHopFilterMax, int64(n))
}

// SetupCircuitBreaker reports whether the route setup node's per-destination
// circuit breaker may open. Off by default — see setupmetrics for why.
func SetupCircuitBreaker() bool { return routersettings.SetupCircuitBreaker.Bool() }

// SetSetupCircuitBreaker turns that lockout on or off.
func SetSetupCircuitBreaker(on bool) bool {
	setBool(routersettings.SetupCircuitBreaker, on)
	return true
}

// SetupCircuitFailThreshold is how many consecutive failures attributed to one
// destination trip its breaker.
func SetupCircuitFailThreshold() int { return routersettings.SetupCircuitFailThreshold.Int() }

// SetSetupCircuitFailThreshold installs that threshold. Non-positive is refused.
func SetSetupCircuitFailThreshold(n int) bool {
	return setInt(routersettings.SetupCircuitFailThreshold, int64(n))
}

// SetupCircuitOpenDuration is how long a tripped breaker refuses setups before
// admitting a half-open probe.
func SetupCircuitOpenDuration() time.Duration {
	return routersettings.SetupCircuitOpenDuration.Duration()
}

// SetSetupCircuitOpenDuration installs that duration. Non-positive is refused.
func SetSetupCircuitOpenDuration(d time.Duration) bool {
	return setInt(routersettings.SetupCircuitOpenDuration, int64(d))
}

// SetupCircuitMaxOpenDuration is the total open/half-open time after which a
// breaker is force-closed.
func SetupCircuitMaxOpenDuration() time.Duration {
	return routersettings.SetupCircuitMaxOpenDuration.Duration()
}

// SetSetupCircuitMaxOpenDuration installs that ceiling. Non-positive is refused.
func SetSetupCircuitMaxOpenDuration(d time.Duration) bool {
	return setInt(routersettings.SetupCircuitMaxOpenDuration, int64(d))
}

// SetupCircuitFailWindow is how close together failures must fall to count as
// consecutive.
func SetupCircuitFailWindow() time.Duration {
	return routersettings.SetupCircuitFailWindow.Duration()
}

// SetSetupCircuitFailWindow installs that window. Non-positive is refused.
func SetSetupCircuitFailWindow(d time.Duration) bool {
	return setInt(routersettings.SetupCircuitFailWindow, int64(d))
}
