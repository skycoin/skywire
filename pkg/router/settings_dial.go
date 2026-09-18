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
