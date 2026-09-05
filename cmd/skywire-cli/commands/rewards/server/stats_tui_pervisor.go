// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_pervisor.go c5-reward-server
//
// How many transports each visor holds, as a histogram.
//
// The network panel says how many transports exist and how many visors
// exist; their ratio is an average that describes nobody. The shape that
// matters is the distribution: a network where every visor holds three
// transports and one where a tenth of the visors hold thirty and the rest
// hold one produce the same average and are not the same network.
//
// WHY THIS PANEL IS GATED AND THE OTHERS ARE NOT.
//
// TPD's transport index oscillates. Adjacent samples of the same aggregate
// have been observed at 14,162 and 30,538 total edges (skycoin/skywire
// #4513) — the index is readable while it is still refilling after a
// restart, and nothing in the HTTP body distinguished a refill from the
// network having halved. A line chart of a fluctuating total at least
// shows the fluctuation. A histogram does not: it renders one sample as a
// static shape, and a sample taken mid-refill draws a network whose visors
// each hold half the transports they hold. That is an artifact presented
// as a finding, which is worse than no panel.
//
// So this panel refuses to draw unless the sample is vouched for, on three
// independent counts:
//
//   - store-level: TransportSummary.Partial / MissingBatches (#4542) — a
//     batched read over the index whose batches did not all return.
//   - publisher-level: the stats feed's complete / confidence stamp
//     (#4526), the trailing-peak verdict on whether the total looks
//     settled or is refilling.
//   - self-consistency: every transport contributes exactly two edges, so
//     the per-key index must sum to twice the aggregate's total. When it
//     does not, the two views were read from different states of the same
//     index — which is the oscillation itself, caught in the act.
//
// Any of the three failing replaces the histogram with a named absence
// carrying the reason, the same convention tuiMissing uses for a fetch
// that died.
package clirewardsserver

import (
	"fmt"
	"sort"
	"strings"
)

// tpPerVisorBucket is one column of the histogram.
type tpPerVisorBucket struct {
	// Label is the bucket as printed ("1", "5-9", "10+").
	Label string
	// Lo and Hi are inclusive bounds; Hi < 0 means unbounded.
	Lo, Hi int
	Visors int
}

// tpPerVisorStats is the panel's data.
type tpPerVisorStats struct {
	Buckets []tpPerVisorBucket
	// Visors is how many visors the per-key index named.
	Visors int
	// Edges is the sum of the per-visor counts. Two per transport.
	Edges int
	// Median and Max describe the tail the buckets compress.
	Median, Max int
	// Gated reports that the sample must not be drawn as a histogram;
	// GateWhy says which check refused it, in the words a reader needs.
	Gated   bool
	GateWhy string
	// Src names where the counts came from, as every other panel does.
	Src string
	// Err is a failure to obtain the counts at all.
	Err string
}

// tpSampleVerdict is what the network aggregate says about itself, lifted
// out of whichever of the two sources answered for it.
type tpSampleVerdict struct {
	// Total is the aggregate's transport count, for the two-edges check.
	Total int
	// Complete and Confidence are the feed's completeness stamp.
	Complete   bool
	Confidence string
	// Partial and MissingBatches are the store's own verdict (#4542).
	Partial        bool
	MissingBatches int
	// Known is false when neither source produced an aggregate to judge
	// against — an unjudged sample is not a passing sample.
	Known bool
}

// tpPerVisorBuckets is the bucketing. Small counts get their own column
// because the difference between one transport and two is the difference
// between a visor with no redundancy and a visor with some; above five the
// distinction stops meaning anything at this resolution.
var tpPerVisorBuckets = []tpPerVisorBucket{
	{Label: "1", Lo: 1, Hi: 1},
	{Label: "2", Lo: 2, Hi: 2},
	{Label: "3", Lo: 3, Hi: 3},
	{Label: "4", Lo: 4, Hi: 4},
	{Label: "5-9", Lo: 5, Hi: 9},
	{Label: "10+", Lo: 10, Hi: -1},
}

// tpEdgeSkewTolerance is how far the per-key index may sum from twice the
// aggregate total before the two are treated as different samples. Not
// zero: the aggregate and the index are separate reads and a transport
// registered between them moves the sum legitimately. One percent is far
// below the swing #4513 produces and far above ordinary churn.
const tpEdgeSkewTolerance = 0.01

// gatherTransportsPerVisor builds the histogram from the per-visor
// transport index, and decides whether it may be drawn.
//
// Failure is recorded, never returned, as everywhere else in this panel.
func gatherTransportsPerVisor(tpdURL string, v tpSampleVerdict) tpPerVisorStats {
	var s tpPerVisorStats

	counts, err := fetchPerKeyTransportCounts(tpdURL)
	if err != nil {
		s.Err = err.Error()
		return s
	}
	if len(counts) == 0 {
		s.Err = "the per-key index named no visors"
		return s
	}
	s = bucketTransportCounts(counts)
	if s.Err != "" {
		return s
	}
	s.Src = "source: HTTP over dmsg /all-transports/per-key-stats" +
		" — no CXO feed carries the per-key index"
	s.Gated, s.GateWhy = tpSampleGate(s.Edges, v)
	return s
}

// bucketTransportCounts turns the per-key index into the histogram. Split
// from the fetch so the shape can be asserted without a network.
func bucketTransportCounts(counts map[string]int) tpPerVisorStats {
	s := tpPerVisorStats{Buckets: make([]tpPerVisorBucket, len(tpPerVisorBuckets))}
	copy(s.Buckets, tpPerVisorBuckets)

	all := make([]int, 0, len(counts))
	for _, n := range counts {
		if n <= 0 {
			continue
		}
		s.Visors++
		s.Edges += n
		if n > s.Max {
			s.Max = n
		}
		all = append(all, n)
		for i := range s.Buckets {
			b := &s.Buckets[i]
			if n >= b.Lo && (b.Hi < 0 || n <= b.Hi) {
				b.Visors++
				break
			}
		}
	}
	if s.Visors == 0 {
		s.Err = "every visor in the per-key index reported zero transports"
		return s
	}
	sort.Ints(all)
	s.Median = all[len(all)/2]
	return s
}

// tpSampleGate applies the three checks. Returns true when the sample must
// not be drawn, with the reason.
func tpSampleGate(edges int, v tpSampleVerdict) (bool, string) {
	if !v.Known {
		return true, "the network aggregate this sample would be checked against could not be " +
			"read, so nothing vouches for the per-key index being a complete one (skywire#4513)"
	}
	if v.Partial {
		return true, fmt.Sprintf("TPD reported the transport index read as PARTIAL — %d batch(es) "+
			"failed, so the counts undercount by an unknown amount (skywire#4542, #4513)",
			v.MissingBatches)
	}
	if !v.Complete {
		conf := v.Confidence
		if conf == "" {
			conf = "unknown"
		}
		return true, fmt.Sprintf("the transport aggregate is stamped INCOMPLETE (confidence %q): "+
			"the index is readable while it refills and a histogram of a refilling index draws "+
			"visors holding fewer transports than they hold (skywire#4513)", conf)
	}
	if v.Total > 0 {
		want := 2 * v.Total
		skew := float64(edges-want) / float64(want)
		if skew < 0 {
			skew = -skew
		}
		if skew > tpEdgeSkewTolerance {
			return true, fmt.Sprintf("the per-key index sums to %d edges against %d transports "+
				"(%d edges expected, %.1f%% apart) — the two views were read from different "+
				"states of the same index, which is the oscillation itself (skywire#4513)",
				edges, v.Total, want, 100*skew)
		}
	}
	return false, ""
}

// renderTPPerVisorPanelANSI draws the histogram, or says why it will not.
func renderTPPerVisorPanelANSI(s tpPerVisorStats) string {
	const title = "TRANSPORTS PER VISOR"
	width := tuiWidth

	if s.Err != "" {
		return tuiMissing(title, s.Err) + "\n"
	}
	if len(s.Buckets) == 0 || s.Visors == 0 {
		return tuiMissing(title, "no data returned") + "\n"
	}
	if s.Gated {
		// Deliberately NOT a drawn histogram with a caveat under it. A
		// rendered shape is read before any footnote is, and the whole
		// point of the gate is that this shape would be wrong.
		return tuiRule(title, width) +
			fmt.Sprintf("  %shistogram withheld%s %sthis sample must not be drawn as a distribution%s\n",
				aRed, aReset, aDim, aReset) +
			tuiWrap("  "+s.GateWhy) +
			tuiWrap(fmt.Sprintf("  %d visors in the index, %d edges — figures kept for "+
				"reference, shape not charted", s.Visors, s.Edges)) +
			tuiSource(s.Src) +
			tuiClose(width) + "\n"
	}

	var b strings.Builder
	b.WriteString(tuiRule(title+" — distribution", width))

	maxVisors := 0
	for _, bk := range s.Buckets {
		if bk.Visors > maxVisors {
			maxVisors = bk.Visors
		}
	}
	for _, bk := range s.Buckets {
		frac := 0.0
		if maxVisors > 0 {
			frac = float64(bk.Visors) / float64(maxVisors)
		}
		share := 0.0
		if s.Visors > 0 {
			share = float64(bk.Visors) / float64(s.Visors)
		}
		col := aGreen
		switch bk.Label {
		case "1":
			// One transport is one failure away from isolated.
			col = aRed
		case "2":
			col = aYellow
		}
		b.WriteString(fmt.Sprintf("  %s%-4s%s %s%5d%s %s %s%5.1f%%%s\n",
			col, bk.Label, aReset, aBold, bk.Visors, aReset,
			tuiBar(frac, 40, col), aDim, 100*share, aReset))
	}
	b.WriteString(fmt.Sprintf("  %s%d visors · median %d · max %d · %d edges%s\n",
		aDim, s.Visors, s.Median, s.Max, s.Edges, aReset))
	// Its own line, not appended to the figures: wrapped away from the
	// numbers it qualifies, this caveat is the one that stops being read.
	b.WriteString(fmt.Sprintf("  %sbars count visors per bucket, not transports%s\n", aDim, aReset))
	b.WriteString(tuiSource(s.Src))
	b.WriteString(tuiClose(width))
	b.WriteString("\n")
	return b.String()
}
