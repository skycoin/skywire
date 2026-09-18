// Package router pkg/router/bottleneck.go c2-net-routing
package router

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
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
// The pong alone is too slow to be useful, though: at one sample per leg per
// legLivenessInterval (30s) the sbdMinSamples floor is only cleared after ~110s,
// and the operator-pinned two-leg-over-one-uplink compositions that SBD exists
// to catch spend that whole time striping across a pipe that is not there. So
// while data flows the SENDER's own feedback is folded too: a SACK yields a
// send→ack delay sample for the leg that carried the frame it NEWLY acks (the
// per-leg ack-delay estimate the RACK threshold already keeps —
// routeMux.recordAckDelayTp), and one such sample per leg per sbdSampleInterval
// goes into the same window, with the same statistics. The two sources measure
// the same thing (a round trip over that leg, queueing included) and a transfer
// floods the window with the SACK source within a second, so the verdict arrives
// in seconds rather than minutes and the park event/reason is unchanged.
//
// NEWLY acks is the whole of it. A SACK purges every entry below its contiguous
// frontier, and those entries were received at some unknown earlier time: their
// send→ack age is how long the FRONTIER took to advance past them, one number
// shared by every leg in the batch. Sampling all of them made the two legs' series
// two views of one quantity — the group's own head-of-line coupling — which
// correlates by construction, on any network. So only the frames whose arrival
// this SACK actually reports are sampled per leg: the frontier-edge entry and the
// bitmap bits set above it (see retxBuffer.ProcessSACKWith).
//
// Because raw cross-correlation would need time-aligned samples (our per-leg
// pongs are sampled independently), we follow RFC 8382 and cluster on the
// distribution-shape summary statistics instead — legs sharing a bottleneck
// have the same statistics even when their samples are not aligned in time.

const (
	// sbdWindowSamples is the moving-window depth (per leg) over which the OWD
	// summary statistics are computed. The per-leg liveness pong lands roughly
	// once per legLivenessInterval (30s), so 8 samples is ~4 minutes of history
	// when the pong is the only source: enough to estimate variance/skew/
	// oscillation, short enough to follow a real path change within a few minutes.
	// While data flows the SACK path feeds the same window at sbdSampleInterval,
	// so the window spans ~0.4s instead — RFC 8382's own timescale — with no change
	// to the math below. Tuning parameter, not an on/off gate.
	sbdWindowSamples = 8
	// sbdMinSamples is the fewest samples a leg needs before its statistics are
	// trusted for grouping. Below it the leg is treated as its OWN singleton group
	// (insufficient evidence to merge — the conservative default: never collapse a
	// leg's capacity on a guess). The default of the --sbd-min-samples knob; the
	// live value is SBDMinSamples().
	sbdMinSamples = 4
	// sbdSampleInterval is the minimum spacing between two per-SACK delay samples
	// folded into ONE leg's window. The SACK path produces samples far faster than
	// the window is meant to summarize (tens per second on a bulk transfer), so
	// without a limiter one burst would overwrite the whole window with samples
	// from a few milliseconds of one queue state. At 50 ms a window of 8 fills in
	// ~0.4 s of sustained transfer and still spans a range of queue states — which
	// is what turns an SBD verdict that needed ~110 s of pongs (one per leg per
	// legLivenessInterval, sbdMinSamples of them) into one that lands within a few
	// seconds of the transfer starting. The default of the --sbd-sample-interval
	// knob; the live value is SBDSampleInterval().
	sbdSampleInterval = 50 * time.Millisecond
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
	minN := SBDMinSamples()
	if a.n < minN || b.n < minN {
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
	return groupLegsBySBDExcept(stats, nil)
}

// groupLegsBySBDExcept is groupLegsBySBD with a veto: blocked(i, j) reports that
// a pair may NOT be merged however alike their statistics look, because a park
// trial already PROVED the two legs carry independent capacity (see the trial
// comment above) and the pair's suppression window has not expired. The veto is
// pairwise rather than per-leg so a leg proven independent of one sibling can
// still be grouped with another.
func groupLegsBySBDExcept(stats []sbdStats, blocked func(i, j int) bool) []int {
	n := len(stats)
	groups := make([]int, n)
	// Union-find parent array; each leg starts as its own root.
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	find := func(x int) int {
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
			if blocked != nil && blocked(i, j) {
				continue
			}
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

// A shared-bottleneck park is a TRIAL, not a verdict.
//
// The detector above rules on delay CO-VARIATION, and co-variation is evidence
// of a shared queue, not proof of one: two routes that leave over different
// first hops and different intermediates can still breathe together (a common
// far-side downlink, a peer's egress shaper, plain diurnal load) while each
// carries its own capacity. When the ruling is wrong the cost is a whole leg —
// measured on the frozen two-leg rig (2026-09-18, bench 195b1094c-sbdsweep,
// 50 MB downloads paired against the best single route): with the detector
// parked out (--sbd-min-samples 1000000) the group ran 9.30 MB/s, x1.13 against
// the paired reference, every row above x1.09; with the default floor of 4 the
// SBD park landed and the same group ran 6.80 MB/s, x0.86. The endpoint's own
// downlink measured 9.55 MB/s over three concurrent single-route clients that
// night, so the two legs were NOT behind one pipe — the ruling was simply wrong,
// and it cost a third of the throughput.
//
// So the park is provisional: GOODPUT arbitrates. The group's aggregate
// delivered-bytes rate over the interval before the park is recorded, the leg is
// parked, and after sbdTrialWindow the rate is read again with the leg out. If it
// FELL by more than sbdTrialLoss the legs were carrying independent capacity — the
// leg is unparked at once (the trial verdict overrides legParkMinHold, which
// exists to damp controllers trading a leg, not to hold a park the evidence just
// refuted) and the PAIR is marked verified-independent: no further SBD ruling may
// merge those two legs for sbdBackoff, doubling on each repeat, capped at
// sbdBackoffMax. If the rate did not fall, the park stands exactly as before —
// one pipe really was being striped twice, and nothing was lost by proving it.
//
// Both ends run this code and a download is EXIT-sent, so the trial runs wherever
// the ruling was made; neither end needs to know about the other's.

const (
	// sbdTrialWindow is how long a park is held before its goodput verdict is
	// read. The readings it compares are the data-progress tick's own per-leg
	// recv deltas (legDataProgressInterval, 5s), so the default of 3s means "the
	// first tick at least 3s after the park" — one full interval of post-park
	// bytes, which is the shortest honest measurement the existing counters
	// support. The default of the --sbd-trial-window knob; the live value is
	// SBDTrialWindow().
	sbdTrialWindow = 3 * time.Second
	// sbdTrialLoss is the fraction of aggregate goodput a park may cost before it
	// is judged wrong. 0.15 sits above the tick-to-tick noise of a bulk transfer
	// (the paired reference rows swing a few percent) and far below the ~27% the
	// misruling above actually cost, so an honest park is never undone and a
	// leg-losing one always is. The default of the --sbd-trial-loss knob; the
	// live value is SBDTrialLoss().
	sbdTrialLoss = 0.15
	// sbdBackoff is how long a pair whose park trial FAILED is exempt from
	// further shared-bottleneck merging. Five minutes is long enough that a
	// transfer does not re-litigate the same wrong verdict every tick and short
	// enough that a genuine bottleneck appearing later is still caught. The
	// default of the --sbd-backoff knob; the live value is SBDBackoff().
	sbdBackoff = 5 * time.Minute
	// sbdBackoffMax caps the doubling applied on each repeat failure for one
	// pair, so a pair the detector keeps misjudging is suppressed for an hour at
	// most and never permanently. Not a knob: it is the ceiling of the knob
	// above, like deadRouteMaxTTL.
	sbdBackoffMax = time.Hour
	// sbdMinEvidenceRate is the aggregate delivered-bytes floor (B/s) a group must
	// be carrying before a shared-bottleneck ruling may park one of its legs.
	//
	// NO TRAFFIC, NO RULING. The detector clusters on delay CO-VARIATION, and an
	// idle group's legs co-vary trivially — a few liveness pongs on two quiet
	// routes look alike because neither is queueing behind anything. Worse, a park
	// decided at idle can never be arbitrated: sbdTrialFailed reads a base rate of
	// zero as no evidence, so the trial cannot fail and the park stands for the
	// whole transfer that follows. That is exactly what the two-leg rig measured
	// (bench/2026-09-16/0251e5da4-smoke/mux-legs-2): the group parked at
	// 01:06:30.615 and re-parked at 01:06:35.615, both BEFORE row 1 moved a byte,
	// and every one of the next 15 rows then ran single-leg — 50 MB downloads at
	// x0.81 and 10 MB at x0.68 against the paired reference, where the same route
	// pair with the detector off ran x1.13.
	//
	// 64 KiB/s is two orders of magnitude below the ~7-9 MB/s a loaded two-leg
	// group carries and still above the handful of bytes a keepalive stirs, so it
	// admits every real transfer and refuses every ruling made on silence. The
	// default of the --sbd-min-evidence-rate knob; the live value is
	// SBDMinEvidenceRate().
	sbdMinEvidenceRate = 64 << 10
	// sbdDemoteDefault is whether a shared-bottleneck ruling may PARK a leg.
	//
	// It is FALSE, and that is a measurement, not caution. Over four rig runs on
	// 2026-09-16/17 (bench/2026-09-16/195b1094c-sbdsweep/{sbd-off,sbd-default},
	// 0251e5da4-smoke, dfb0755c2-smoke) the detector ruled eight times and was
	// right zero times: with parking off (--sbd-min-samples raised out of reach on
	// both ends) the two-leg 50 MB downloads ran x1.13 against the paired
	// reference, 5 of 5 at or above x1.09; with parking on at its defaults the same
	// route pair ran x0.86, and the parks decided while the group was idle were
	// permanent at x0.81. With the evidence floor in place three of the four
	// compose-set parks were refuted by their own trial — two of them on idle ticks
	// between bench rows, trial rate 2 B/s — and the one that stood cost 10.8%.
	//
	// So the ruling is kept and recorded (MuxEventSBDRuling, with the correlation
	// numbers and the rate behind it) and the grouping still reaches the mux, where
	// it only makes rebuildWeights count one bottleneck as one unit of capacity.
	// The demotion — the part that takes a leg away — waits for an operator:
	// `skywire cli route settings --sbd-demote true`. The default of the
	// --sbd-demote knob; the live value is SBDDemote().
	sbdDemoteDefault = false
)

// sbdAggRate turns one data-progress tick's per-leg recv deltas into the group's
// aggregate delivered-bytes rate in B/s — the quantity a park trial arbitrates
// on. Pure.
func sbdAggRate(recvDeltas map[uuid.UUID]uint64) float64 {
	if len(recvDeltas) == 0 {
		return 0
	}
	var total uint64
	for _, d := range recvDeltas {
		total += d
	}
	return float64(total) / legDataProgressInterval.Seconds()
}

// sbdTrialFailed reports whether a park COST the group goodput: the aggregate
// rate measured with the leg parked fell more than loss below the rate measured
// over the interval before the park. A non-positive base rate is no evidence at
// all (the group was idle when the park landed), so a trial can never fail on
// it. sbdMinEvidenceRate stops such a park being decided in the first place, and
// evaluateSBDTrials holds any that slipped through OPEN until traffic arrives
// rather than letting it stand — see the idle branch there. Pure.
func sbdTrialFailed(baseRate, trialRate, loss float64) bool {
	if baseRate <= 0 {
		return false
	}
	return trialRate < baseRate*(1-loss)
}

// nextSBDBackoff returns the suppression window for a pair whose park trial just
// failed: SBDBackoff() the first time, double the previous window on each
// repeat, capped at sbdBackoffMax. Pure.
func nextSBDBackoff(prev time.Duration) time.Duration {
	if prev <= 0 {
		return SBDBackoff()
	}
	d := prev * 2
	if d > sbdBackoffMax {
		d = sbdBackoffMax
	}
	return d
}

// sbdPairKey identifies an unordered pair of legs by their transport IDs
// (transport IDs survive the leg-index shifts a rebuild causes).
type sbdPairKey struct{ a, b uuid.UUID }

// sbdPair normalizes two leg transport IDs into an order-independent key.
func sbdPair(x, y uuid.UUID) sbdPairKey {
	if bytes.Compare(x[:], y[:]) > 0 {
		x, y = y, x
	}
	return sbdPairKey{a: x, b: y}
}

// sbdGroupKeeper returns the index of the ACTIVE leg a parked leg was judged
// redundant against — the member of its shared-bottleneck group that
// pickBottleneckDemotions kept — or -1 when there is none. That leg is the other
// half of the pair a failed trial marks verified-independent. Pure.
func sbdGroupKeeper(legs []bottleneckLeg, demote []int, idx int) int {
	parked := make(map[int]struct{}, len(demote))
	for _, d := range demote {
		parked[d] = struct{}{}
	}
	group, found := 0, false
	for _, l := range legs {
		if l.idx == idx {
			group, found = l.group, true
			break
		}
	}
	if !found {
		return -1
	}
	for _, l := range legs {
		if l.idx == idx || l.standby || l.group != group {
			continue
		}
		if _, off := parked[l.idx]; off {
			continue
		}
		return l.idx
	}
	return -1
}

// sbdRulingReason renders one shared-bottleneck ruling as the sentence recorded
// in a MuxEventSBDRuling: which leg the detector judged redundant, which active
// leg it was judged against, the summary statistics that made them look alike,
// and the send-path rate the reading was taken at. keeper < 0 (no surviving
// active leg found in the group) drops the pairwise half.
//
// It exists because with demotion off (sbdDemoteDefault) the ruling IS the whole
// output: a bench run scores the detector by reading these against what the
// transfer did, so the numbers behind the verdict have to be in the record, not
// just the verdict. Pure.
func sbdRulingReason(idx, keeper int, groups []int, stats []sbdStats, rate, floor float64) string {
	group := -1
	if idx >= 0 && idx < len(groups) {
		group = groups[idx]
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "shared-bottleneck ruling (advisory — sbd_demote is off, nothing was parked): leg %d", idx)
	if keeper >= 0 && idx < len(stats) && keeper < len(stats) {
		a, k := stats[idx], stats[keeper]
		fmt.Fprintf(&b, " reads as co-bottlenecked with active leg %d in group %d — skew %.3f vs %.3f, cv %.3f vs %.3f, freq %.3f vs %.3f, mean %.1f vs %.1f ms over %d/%d samples",
			keeper, group, a.skew, k.skew, a.cv, k.cv, a.freq, k.freq, a.mean, k.mean, a.n, k.n)
	} else {
		fmt.Fprintf(&b, " reads as co-bottlenecked with a kept active leg in group %d", group)
	}
	fmt.Fprintf(&b, "; send-path delivered %.0f B/s (evidence floor %.0f B/s). Set --sbd-demote true on both ends to act on rulings like this one.", rate, floor)
	return b.String()
}
