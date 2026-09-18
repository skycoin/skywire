// Package router pkg/router/settings.go c2-net-routing
//
// Runtime-settable router knobs — the send-window shape and the adaptive park
// hold — behind plain atomics, so `skywire cli route settings` can move them on
// a running visor instead of a rebuild and a fleet deploy.
//
// The pattern is pkg/router/policy/preset's (SetAdaptCap and friends): a
// package-level atomic seeded with the constant the package compiled with, read
// at the use site on every pass. Every default here IS the old constant, so a
// visor that never sets one behaves byte for byte as it did.
//
// Only knobs whose use site already re-reads the value each pass live here.
// windowRefreshInterval is deliberately NOT one: it is handed to
// servicePacketLoop when a route group is built and becomes that goroutine's
// ticker, so changing it live would mean reaching into every live group's
// loop — a locking change, not a knob.
package router

import (
	"math"
	"sync/atomic"
	"time"
)

var (
	// ecfWindowMarginV, ecfMinWindowBytesV, ecfMaxWindowBytesV and
	// sendWindowWaitMaxV shadow the constants of the same name in
	// route_mux.go, which remain the documented defaults.
	ecfWindowMarginV   atomic.Uint64 // float64 bits
	ecfMinWindowBytesV atomic.Int64
	ecfMaxWindowBytesV atomic.Int64
	sendWindowWaitMaxV atomic.Int64 // nanoseconds
	legParkMinHoldV    atomic.Int64 // nanoseconds

	// deadRouteHoldV and deadRouteHoldMaxV shadow deadRouteTTL and
	// deadRouteMaxTTL in dead_route_cache.go; the cache re-reads them on every
	// death, so a change reaches the router already running.
	deadRouteHoldV    atomic.Int64 // nanoseconds
	deadRouteHoldMaxV atomic.Int64 // nanoseconds

	// sbdMinSamplesV and sbdSampleIntervalV shadow sbdMinSamples and
	// sbdSampleInterval in bottleneck.go. sbdSimilar re-reads the first on every
	// pairwise test and the per-SACK fold re-reads the second on every sample,
	// so a sweep can move either on a running visor.
	sbdMinSamplesV     atomic.Int64
	sbdSampleIntervalV atomic.Int64 // nanoseconds

	// sbdTrialWindowV, sbdTrialLossV and sbdBackoffV shadow sbdTrialWindow,
	// sbdTrialLoss and sbdBackoff in bottleneck.go — the terms of the park TRIAL
	// that qualifies every shared-bottleneck ruling. The trial evaluator re-reads
	// all three on every data-progress tick, so a sweep can move them on a running
	// visor; both ends of a group must carry the same code, not the same values.
	sbdTrialWindowV atomic.Int64  // nanoseconds
	sbdTrialLossV   atomic.Uint64 // float64 bits
	sbdBackoffV     atomic.Int64  // nanoseconds

	// sbdMinEvidenceRateV shadows sbdMinEvidenceRate in bottleneck.go — the
	// aggregate goodput a group must be carrying before a shared-bottleneck
	// ruling may park one of its legs. enforceBottleneckGroups re-reads it on
	// every data-progress tick.
	sbdMinEvidenceRateV atomic.Int64 // bytes per second

	// sbdDemoteV shadows sbdDemoteDefault in bottleneck.go — whether a
	// shared-bottleneck ruling may PARK a leg at all. OFF by default: on the
	// 2026-09-16/17 rig the detector was 0-for-8 (see bottleneck.go), so the
	// ruling is recorded as a mux event and nothing else changes — neither the
	// active set nor the send weights, since the grouping is itself a weight
	// decision — until an operator turns this on. enforceBottleneckGroups
	// re-reads it on every data-progress tick.
	sbdDemoteV atomic.Bool

	// forwardSpillV and forwardSwitchMarginV govern the FORWARD direction's
	// confinement to one leg (see route_mux.go selectConfinedForward). Both are
	// re-read on every pick, so a sweep can move them on a running visor.
	forwardSpillV        atomic.Bool
	forwardSwitchMarginV atomic.Uint64 // float64 bits

	// legStarveRatioV and legProbeBytesV shadow legStarveRatio and legProbeBytes
	// in route_mux.go — the terms of the OUTCLASSED-LEG gate. The ratio is
	// re-read by every window refresh (ruleProbeOnlyLegsLocked) and the budget by
	// every pick that lands on a probe-only leg (legProbeExhausted), so a sweep
	// can move either on a running visor; a ratio at or below 1 turns the gate
	// off entirely.
	legStarveRatioV atomic.Uint64 // float64 bits
	legProbeBytesV  atomic.Int64
)

func init() {
	ecfWindowMarginV.Store(math.Float64bits(ecfWindowMargin))
	ecfMinWindowBytesV.Store(ecfMinWindowBytes)
	ecfMaxWindowBytesV.Store(ecfMaxWindowBytes)
	sendWindowWaitMaxV.Store(int64(sendWindowWaitMax))
	legParkMinHoldV.Store(int64(legParkMinHold))
	deadRouteHoldV.Store(int64(deadRouteTTL))
	deadRouteHoldMaxV.Store(int64(deadRouteMaxTTL))
	sbdMinSamplesV.Store(sbdMinSamples)
	sbdSampleIntervalV.Store(int64(sbdSampleInterval))
	sbdTrialWindowV.Store(int64(sbdTrialWindow))
	sbdTrialLossV.Store(math.Float64bits(sbdTrialLoss))
	sbdBackoffV.Store(int64(sbdBackoff))
	sbdMinEvidenceRateV.Store(sbdMinEvidenceRate)
	sbdDemoteV.Store(sbdDemoteDefault)
	forwardSpillV.Store(forwardSpillDefault)
	forwardSwitchMarginV.Store(math.Float64bits(forwardSwitchMarginDefault))
	legStarveRatioV.Store(math.Float64bits(legStarveRatio))
	legProbeBytesV.Store(legProbeBytes)
}

// LegStarveRatio is the one ratio the outclassed-leg ruling uses at both ends:
// a leg is cut to a probe per window only when its delay basis exceeds the best
// ready leg's by more than this AND its proven delivery rate is under 1/this of
// that leg's — so a leg that is slow but productive keeps its share. At or below
// 1 the gate is off.
func LegStarveRatio() float64 { return math.Float64frombits(legStarveRatioV.Load()) }

// SetLegStarveRatio installs that ratio. Only a finite value above 1 is
// accepted — at or below 1 every leg would outclass every other, including
// itself; use a very large ratio to disable the gate instead.
func SetLegStarveRatio(v float64) bool {
	if v <= 1 || math.IsInf(v, 0) || math.IsNaN(v) {
		return false
	}
	legStarveRatioV.Store(math.Float64bits(v))
	return true
}

// LegProbeBytes is how many bytes a leg ruled probe-only may carry per window
// (the window being its own delay basis, floored at legProbeMinWindow).
func LegProbeBytes() int64 { return legProbeBytesV.Load() }

// SetLegProbeBytes installs that budget. Non-positive is refused: a budget of
// zero would silence the leg completely, and the point of the probe is that the
// leg keeps being measured so the ruling can lift.
func SetLegProbeBytes(v int64) bool {
	if v <= 0 {
		return false
	}
	legProbeBytesV.Store(v)
	return true
}

// EcfWindowMargin is the multiplier on SACK-proven delivery-per-RTT that sets a
// leg's send window.
func EcfWindowMargin() float64 { return math.Float64frombits(ecfWindowMarginV.Load()) }

// SetEcfWindowMargin installs the send-window margin. Non-positive is refused.
func SetEcfWindowMargin(v float64) bool {
	if v <= 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return false
	}
	ecfWindowMarginV.Store(math.Float64bits(v))
	return true
}

// EcfMinWindowBytes is the floor a leg's send window is clamped to.
func EcfMinWindowBytes() int64 { return ecfMinWindowBytesV.Load() }

// SetEcfMinWindowBytes installs the send-window floor. Non-positive is refused.
func SetEcfMinWindowBytes(v int64) bool {
	if v <= 0 {
		return false
	}
	ecfMinWindowBytesV.Store(v)
	return true
}

// EcfMaxWindowBytes is the ceiling a leg's send window is clamped to — the
// number the client-side chunk and upload sizes are sized against.
func EcfMaxWindowBytes() int64 { return ecfMaxWindowBytesV.Load() }

// SetEcfMaxWindowBytes installs the send-window ceiling. Non-positive is refused.
func SetEcfMaxWindowBytes(v int64) bool {
	if v <= 0 {
		return false
	}
	ecfMaxWindowBytesV.Store(v)
	return true
}

// SendWindowWaitMax bounds how long a writer parks when every ready leg is at
// its window before sending anyway.
func SendWindowWaitMax() time.Duration { return time.Duration(sendWindowWaitMaxV.Load()) }

// SetSendWindowWaitMax installs the send-window wait bound. Non-positive is refused.
func SetSendWindowWaitMax(d time.Duration) bool {
	if d <= 0 {
		return false
	}
	sendWindowWaitMaxV.Store(int64(d))
	return true
}

// LegParkMinHold is how long an adaptive park holds before any adaptive
// promoter may re-admit the leg.
func LegParkMinHold() time.Duration { return time.Duration(legParkMinHoldV.Load()) }

// SetLegParkMinHold installs the adaptive park hold. Non-positive is refused.
func SetLegParkMinHold(d time.Duration) bool {
	if d <= 0 {
		return false
	}
	legParkMinHoldV.Store(int64(d))
	return true
}

// SetMuxFEC turns FEC advertisement on mux route groups on or off for route
// groups created from now on. Live in the sense the rest of this file is not:
// FEC is negotiated when a group is built, so a group already running keeps the
// setting it was born with.
func (r *router) SetMuxFEC(on bool) { r.muxFEC.Store(on) }

// GetMuxFEC reports whether new mux route groups advertise FEC.
func (r *router) GetMuxFEC() bool { return r.muxFEC.Load() }

// DeadRouteHold is the first exclusion window a route that died young is kept
// out of the diversify search for.
func DeadRouteHold() time.Duration { return time.Duration(deadRouteHoldV.Load()) }

// SetDeadRouteHold installs the first exclusion window. Non-positive is refused.
func SetDeadRouteHold(d time.Duration) bool {
	if d <= 0 {
		return false
	}
	deadRouteHoldV.Store(int64(d))
	return true
}

// DeadRouteHoldMax caps the doubling applied on each repeat death.
func DeadRouteHoldMax() time.Duration { return time.Duration(deadRouteHoldMaxV.Load()) }

// SetDeadRouteHoldMax installs the exclusion ceiling. Non-positive is refused.
func SetDeadRouteHoldMax(d time.Duration) bool {
	if d <= 0 {
		return false
	}
	deadRouteHoldMaxV.Store(int64(d))
	return true
}

// SBDMinSamples is how many delay samples a leg needs before its statistics are
// trusted for shared-bottleneck grouping.
func SBDMinSamples() int { return int(sbdMinSamplesV.Load()) }

// SetSBDMinSamples installs the shared-bottleneck sample floor. Non-positive is
// refused.
func SetSBDMinSamples(n int) bool {
	if n <= 0 {
		return false
	}
	sbdMinSamplesV.Store(int64(n))
	return true
}

// SBDSampleInterval is the minimum spacing between two per-SACK delay samples
// folded into one leg's shared-bottleneck window.
func SBDSampleInterval() time.Duration { return time.Duration(sbdSampleIntervalV.Load()) }

// SetSBDSampleInterval installs that spacing. Non-positive is refused.
func SetSBDSampleInterval(d time.Duration) bool {
	if d <= 0 {
		return false
	}
	sbdSampleIntervalV.Store(int64(d))
	return true
}

// SBDTrialWindow is how long a shared-bottleneck park is held as a trial before
// the group's aggregate goodput is re-read to judge it.
func SBDTrialWindow() time.Duration { return time.Duration(sbdTrialWindowV.Load()) }

// SetSBDTrialWindow installs the park-trial window. Non-positive is refused.
func SetSBDTrialWindow(d time.Duration) bool {
	if d <= 0 {
		return false
	}
	sbdTrialWindowV.Store(int64(d))
	return true
}

// SBDTrialLoss is the fraction of aggregate goodput a shared-bottleneck park may
// cost before the park is undone and the pair marked verified-independent.
func SBDTrialLoss() float64 { return math.Float64frombits(sbdTrialLossV.Load()) }

// SetSBDTrialLoss installs that fraction. Only a value in (0, 1) is accepted: 0
// would undo every park on measurement noise and 1 could never be reached.
func SetSBDTrialLoss(v float64) bool {
	if v <= 0 || v >= 1 || math.IsNaN(v) {
		return false
	}
	sbdTrialLossV.Store(math.Float64bits(v))
	return true
}

// SBDBackoff is the first window a pair whose park trial failed is exempt from
// shared-bottleneck merging for; it doubles on each repeat, capped at
// sbdBackoffMax.
func SBDBackoff() time.Duration { return time.Duration(sbdBackoffV.Load()) }

// SetSBDBackoff installs that first window. Non-positive is refused.
func SetSBDBackoff(d time.Duration) bool {
	if d <= 0 {
		return false
	}
	sbdBackoffV.Store(int64(d))
	return true
}

// SBDMinEvidenceRate is the aggregate delivered-bytes rate (B/s) a route group
// must be carrying before a shared-bottleneck ruling may park one of its legs.
// Below it there is no traffic to have produced a bottleneck and none to
// arbitrate the park with, so the ruling is withheld.
func SBDMinEvidenceRate() int64 { return sbdMinEvidenceRateV.Load() }

// SetSBDMinEvidenceRate installs that floor. Non-positive is refused: a floor of
// zero is what let an idle park stand for a whole transfer.
func SetSBDMinEvidenceRate(v int64) bool {
	if v <= 0 {
		return false
	}
	sbdMinEvidenceRateV.Store(v)
	return true
}

// SBDDemote reports whether a shared-bottleneck ruling may park a leg. When it
// is off (the default) the detector still groups the legs and hands the grouping
// to the mux, and every ruling it would have acted on is recorded as a
// MuxEventSBDRuling mux event — but no leg is demoted.
func SBDDemote() bool { return sbdDemoteV.Load() }

// SetSBDDemote turns shared-bottleneck DEMOTION on or off on a running visor.
// Unlike the numeric knobs there is no "zero means unchanged" case, so this
// always applies and always reports true.
func SetSBDDemote(on bool) bool {
	sbdDemoteV.Store(on)
	return true
}

// forwardSpillDefault is OFF: the forward direction stays on its confined leg
// and a full send window WAITS (bounded by SendWindowWaitMax) instead of
// spilling the frame onto another leg. Every live row that did spill — the
// upload split across a 44 ms and a 166 ms leg — collapsed, at x0.68 for 10 MB
// and x0.24 for 50 MB, while the rows that stayed on one leg ran at x0.98-1.0.
const forwardSpillDefault = false

// ForwardSpill reports whether a forward frame may leave the confined leg when
// that leg is at its send window. Off by default; on restores the pre-fix
// behavior, where a saturated confined leg handed the frame to the scheduler.
func ForwardSpill() bool { return forwardSpillV.Load() }

// SetForwardSpill turns forward spill on or off on a running visor. Like
// SetSBDDemote there is no "zero means unchanged" case, so it always applies.
func SetForwardSpill(on bool) bool {
	forwardSpillV.Store(on)
	return true
}

// ForwardSwitchMargin is how much LOWER a challenger leg's measured end-to-end
// latency must be before the forward direction moves off the leg it holds
// (0.2 = 20 % lower), for forwardSwitchSamples consecutive samples.
func ForwardSwitchMargin() float64 { return math.Float64frombits(forwardSwitchMarginV.Load()) }

// SetForwardSwitchMargin installs that fraction. Only a value in (0, 1) is
// accepted: 0 would move the direction on measurement noise — the flapping this
// margin exists to stop — and 1 could never be cleared.
func SetForwardSwitchMargin(v float64) bool {
	if v <= 0 || v >= 1 || math.IsNaN(v) {
		return false
	}
	forwardSwitchMarginV.Store(math.Float64bits(v))
	return true
}
