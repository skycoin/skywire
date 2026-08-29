// Package router pkg/router/bottleneck.go c2-net-routing
package router

import (
	"math"
	"sort"
)

// Shared-bottleneck detection (SBD) for mux legs, after RFC 8382.
//
// Two mux legs that ride different transports can still funnel through the same
// physical uplink (the visor's own access link, a shared upstream hop). When
// they do, the scheduler counts them as N independent pipes and stripes across
// all N — but there is really ONE pipe, so the legs merely compete: they add
// reorder cost and self-inflicted queuing for zero extra capacity. The
// structural same-LAN reject (#4253) only catches co-located INTERMEDIATES; it
// is blind to two disjoint routes that share an uplink further out.
//
// RFC 8382 detects a shared bottleneck from the VARIATION in one-way delay
// (OWD), not its absolute value: legs behind the same congested queue exhibit
// the same delay-variation SIGNATURE (variance, skewness, oscillation), because
// they queue behind the same buffer. It needs neither synchronized clocks nor
// time-aligned samples — only summary statistics of each leg's own OWD series —
// which is exactly what the per-leg liveness pong already gives us (a raw
// round-trip sample per leg per tick, folded here BEFORE the EWMA). RTT stands
// in for OWD: the variation we key on is dominated by queueing, which RTT and
// OWD share. No new probe traffic is added.
//
// Because raw cross-correlation would need time-aligned samples (our per-leg
// pongs are sampled independently), we follow RFC 8382 and cluster on the
// distribution-shape summary statistics instead — legs sharing a bottleneck
// have the same statistics even when their samples are not aligned in time.

const (
	// sbdWindowSamples is the moving-window depth (per leg) over which the OWD
	// summary statistics are computed. The per-leg liveness pong lands roughly
	// once per legLivenessInterval (30s), so 8 samples is ~4 minutes of history:
	// enough to estimate variance/skew/oscillation, short enough to follow a real
	// path change within a few minutes. A finer per-leg OWD sampler (were one ever
	// added) would let this window shrink toward RFC 8382's ~15s without any change
	// to the math below. Tuning parameter, not an on/off gate.
	sbdWindowSamples = 8
	// sbdMinSamples is the fewest samples a leg needs before its statistics are
	// trusted for grouping. Below it the leg is treated as its OWN singleton group
	// (insufficient evidence to merge — the conservative default: never collapse a
	// leg's capacity on a guess).
	sbdMinSamples = 4
	// sbdSkewTol is the maximum |skew_i - skew_j| for two legs to be judged
	// co-bottlenecked. skew_est is in [-1, 1] (fraction of samples below the mean
	// minus fraction above), so 0.5 is a half-scale band — legs behind the same
	// queue share a skew sign and rough magnitude.
	sbdSkewTol = 0.5
	// sbdCVTolFrac is the maximum RELATIVE difference in coefficient of variation
	// (stddev/mean) for two legs to be judged co-bottlenecked. Using CV rather than
	// raw variance makes the test scale-invariant, so a fast and a slow leg behind
	// the same bottleneck (different base RTT, same fractional jitter) still match.
	sbdCVTolFrac = 0.5
	// sbdFreqTol is the maximum |freq_i - freq_j| for two legs to be judged
	// co-bottlenecked. freq_est is the fraction of consecutive-sample transitions
	// that cross the mean (0..1) — the oscillation frequency of the queue. 0.4 is a
	// wide band appropriate to the small windows the coarse pong cadence yields.
	sbdFreqTol = 0.4
)

// sbdWindow is a fixed-capacity ring of a single leg's most recent raw OWD (RTT)
// samples, in milliseconds. Not safe for concurrent use; the caller serializes
// pushes and reads under its own lock (RouteGroup.legLivenessMu).
type sbdWindow struct {
	buf  []float64
	next int
	n    int
}

// newSBDWindow returns an empty window sized to sbdWindowSamples.
func newSBDWindow() *sbdWindow {
	return &sbdWindow{buf: make([]float64, sbdWindowSamples)}
}

// push appends one raw OWD sample (ms), overwriting the oldest when full.
// Non-positive samples are ignored (a bad/late pong is already rejected upstream,
// this is belt-and-suspenders so a stray zero never skews the stats).
func (w *sbdWindow) push(sampleMs float64) {
	if w == nil || sampleMs <= 0 {
		return
	}
	if len(w.buf) == 0 {
		w.buf = make([]float64, sbdWindowSamples)
	}
	w.buf[w.next] = sampleMs
	w.next = (w.next + 1) % len(w.buf)
	if w.n < len(w.buf) {
		w.n++
	}
}

// samples returns a copy of the window's live samples in chronological order
// (oldest first), so callers can compute order-dependent statistics (freq_est).
func (w *sbdWindow) samples() []float64 {
	if w == nil || w.n == 0 {
		return nil
	}
	out := make([]float64, 0, w.n)
	if w.n < len(w.buf) {
		// Not yet wrapped: live samples are buf[0:n] in order.
		out = append(out, w.buf[:w.n]...)
		return out
	}
	// Wrapped: oldest is at next, read around the ring.
	for i := 0; i < len(w.buf); i++ {
		out = append(out, w.buf[(w.next+i)%len(w.buf)])
	}
	return out
}

// sbdStats holds the RFC 8382 summary statistics of one leg's OWD window.
type sbdStats struct {
	// n is how many samples the statistics were computed over.
	n int
	// mean is the mean OWD (ms).
	mean float64
	// variance is the population variance of the OWD samples (ms^2).
	variance float64
	// cv is the coefficient of variation (stddev/mean), the scale-invariant
	// spread used for grouping. 0 when mean<=0.
	cv float64
	// skew is RFC 8382's skew_est: (#below-mean - #above-mean)/n, in [-1, 1].
	// A congested bottleneck queue spends most time near its base delay with
	// occasional upward spikes, so skew tends POSITIVE behind a real bottleneck.
	skew float64
	// freq is RFC 8382's freq_est proxy: the fraction of consecutive-sample steps
	// that cross the mean (0..1) — the queue's oscillation frequency.
	freq float64
}

// computeSBDStats computes the summary statistics of one leg's OWD samples
// (chronological order). Returns n=0 (and zero stats) for an empty input.
func computeSBDStats(samples []float64) sbdStats {
	n := len(samples)
	if n == 0 {
		return sbdStats{}
	}
	var sum float64
	for _, s := range samples {
		sum += s
	}
	mean := sum / float64(n)

	var sqSum float64
	var below, above int
	for _, s := range samples {
		d := s - mean
		sqSum += d * d
		// A small dead-band around the mean keeps float noise on a flat series
		// from registering as skew/crossings.
		switch {
		case s < mean-sbdMeanEps(mean):
			below++
		case s > mean+sbdMeanEps(mean):
			above++
		}
	}
	variance := sqSum / float64(n)
	std := math.Sqrt(variance)
	cv := 0.0
	if mean > 0 {
		cv = std / mean
	}
	skew := float64(below-above) / float64(n)

	// freq_est: fraction of consecutive steps whose (sample-mean) sign flips.
	crossings := 0
	if n > 1 {
		prev := samples[0] - mean
		for i := 1; i < n; i++ {
			cur := samples[i] - mean
			if (prev < 0 && cur > 0) || (prev > 0 && cur < 0) {
				crossings++
			}
			// Carry the last non-zero sign so a sample sitting exactly on the mean
			// doesn't reset the oscillation count.
			if cur != 0 {
				prev = cur
			}
		}
	}
	freq := 0.0
	if n > 1 {
		freq = float64(crossings) / float64(n-1)
	}

	return sbdStats{n: n, mean: mean, variance: variance, cv: cv, skew: skew, freq: freq}
}

// sbdMeanEps is the dead-band half-width around the mean used when classifying a
// sample as above/below/at the mean, as a tiny fraction of the mean plus an
// absolute floor for near-zero means.
func sbdMeanEps(mean float64) float64 {
	return math.Max(0.001, 1e-6*math.Abs(mean))
}

// sbdSimilar reports whether two legs' statistics indicate a SHARED bottleneck:
// both have enough samples AND their skew, coefficient of variation, and
// oscillation frequency all fall within tolerance. All three must agree — the
// conjunction is what makes an accidental match on any single axis insufficient
// to collapse a leg's capacity.
func sbdSimilar(a, b sbdStats) bool {
	if a.n < sbdMinSamples || b.n < sbdMinSamples {
		return false
	}
	if math.Abs(a.skew-b.skew) > sbdSkewTol {
		return false
	}
	if math.Abs(a.freq-b.freq) > sbdFreqTol {
		return false
	}
	maxCV := math.Max(a.cv, b.cv)
	if maxCV < 1e-9 {
		// Both series are essentially flat (no measurable variation): with no
		// delay-variation signature there is no evidence of a shared queue, so do
		// NOT merge. Flat legs stay independent.
		return false
	}
	if math.Abs(a.cv-b.cv)/maxCV > sbdCVTolFrac {
		return false
	}
	return true
}

// groupLegsBySBD partitions legs into shared-bottleneck groups from their OWD
// summary statistics and returns a group-id slice PARALLEL to stats: legs with
// the same group id are judged to share a bottleneck. Group ids are the smallest
// leg index in each group (stable, deterministic).
//
// Heuristic: single-linkage clustering (union-find) over the pairwise sbdSimilar
// relation. Two legs join when their statistics agree on all three axes; the
// union then merges their groups transitively. Single-linkage can in principle
// chain (A~B, B~C but A not ~C all land together); with the handful of legs a mux
// carries this is acceptable and documented — the practical effect is only ever a
// more CONSERVATIVE capacity estimate (never fewer distinct pipes than reality).
// A leg with too few samples is its own singleton (never merged): absent
// evidence, its capacity is counted in full.
func groupLegsBySBD(stats []sbdStats) []int {
	n := len(stats)
	groups := make([]int, n)
	// Union-find parent array; each leg starts as its own root.
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		// Point the larger root at the smaller so the canonical id is the smallest
		// index in the merged set.
		if ra < rb {
			parent[rb] = ra
		} else {
			parent[ra] = rb
		}
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if sbdSimilar(stats[i], stats[j]) {
				union(i, j)
			}
		}
	}
	for i := 0; i < n; i++ {
		groups[i] = find(i)
	}
	return groups
}

// bottleneckLeg is one leg's input to the distinct-group admission decision.
type bottleneckLeg struct {
	idx     int
	group   int    // shared-bottleneck group id (from groupLegsBySBD)
	standby bool   // already a warm standby (only ACTIVE legs are considered)
	primary bool   // the primary leg (index 0) — never parked, always the keeper
	goodput uint64 // recent delivered bytes; higher = better keeper
	latMs   float64
}

// pickBottleneckDemotions returns the indices of ACTIVE legs to park to warm
// standby so at most ONE active leg remains per shared-bottleneck group — the
// admission rule "prefer legs from DISTINCT groups". Striping two legs that share
// one pipe buys no capacity and only adds reorder cost, so the redundant members
// are parked (kept warm for failover, promotable instantly). Per group the keeper
// is: the primary if the group contains it, else the highest-goodput member,
// tie-broken by lowest latency then lowest index. The primary is never parked and
// a group with a single active member is left alone. Pure (no locks / rg state)
// so it is unit-tested directly.
//
// This SUBSUMES the same-LAN structural reject (#4253): two legs whose disjoint
// routes funnel through one uplink land in the same group here and all but one are
// parked, which the mux-set structural check (co-located INTERMEDIATE only) cannot
// see. The structural check is retained as a cheap pre-filter at leg-creation
// time; this is the general runtime rule.
func pickBottleneckDemotions(legs []bottleneckLeg) []int {
	// Bucket active legs by group.
	byGroup := make(map[int][]bottleneckLeg)
	for _, l := range legs {
		if l.standby {
			continue
		}
		byGroup[l.group] = append(byGroup[l.group], l)
	}
	var demote []int
	for _, members := range byGroup {
		if len(members) < 2 {
			continue // a distinct pipe with one active leg — nothing to collapse
		}
		keeper := -1
		for i, m := range members {
			switch {
			case m.primary:
				keeper = i // primary always wins
			case keeper == -1:
				keeper = i
			case members[keeper].primary:
				// keeper already the primary; leave it
			case betterKeeper(m, members[keeper]):
				keeper = i
			}
		}
		for i, m := range members {
			if i == keeper || m.primary {
				continue
			}
			demote = append(demote, m.idx)
		}
	}
	sort.Ints(demote)
	return demote
}

// betterKeeper reports whether leg a is a better active representative than b:
// higher goodput, then lower latency, then lower index.
func betterKeeper(a, b bottleneckLeg) bool {
	if a.goodput != b.goodput {
		return a.goodput > b.goodput
	}
	if a.latMs > 0 && b.latMs > 0 && a.latMs != b.latMs {
		return a.latMs < b.latMs
	}
	return a.idx < b.idx
}
