// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_pervisor_test.go c5-reward-server
package clirewardsserver

import (
	"fmt"
	"strings"
	"testing"
)

// evenCounts builds a per-key index of `visors` visors each holding `n`
// transports, keyed by full-length public keys.
func evenCounts(visors, n int) map[string]int {
	out := make(map[string]int, visors)
	for i := 0; i < visors; i++ {
		out[fmt.Sprintf("%066x", i)] = n
	}
	return out
}

// vouched returns a verdict that passes every gate for the given index.
func vouched(edges int) tpSampleVerdict {
	return tpSampleVerdict{Total: edges / 2, Complete: true, Confidence: "settled", Known: true}
}

// The bars are visors per bucket. Getting this wrong — counting transports
// instead of visors — would draw a plausible shape describing nothing.
func TestPerVisorBucketsCountVisorsNotTransports(t *testing.T) {
	counts := map[string]int{
		strings.Repeat("a", 66): 1,
		strings.Repeat("b", 66): 1,
		strings.Repeat("c", 66): 3,
		strings.Repeat("d", 66): 7,
		strings.Repeat("e", 66): 40,
	}
	s := bucketTransportCounts(counts)
	if s.Visors != 5 {
		t.Errorf("counted %d visors, want 5", s.Visors)
	}
	if s.Edges != 52 {
		t.Errorf("summed %d edges, want 52", s.Edges)
	}
	if s.Max != 40 {
		t.Errorf("max is %d, want 40", s.Max)
	}
	want := map[string]int{"1": 2, "2": 0, "3": 1, "4": 0, "5-9": 1, "10+": 1}
	for _, b := range s.Buckets {
		if b.Visors != want[b.Label] {
			t.Errorf("bucket %q holds %d visors, want %d", b.Label, b.Visors, want[b.Label])
		}
	}
}

// A store-level partial read (#4542) is a KNOWN undercount. It must stop
// the histogram outright, not annotate it.
func TestPerVisorPanelWithholdsAPartialSample(t *testing.T) {
	s := bucketTransportCounts(evenCounts(100, 4))
	s.Gated, s.GateWhy = tpSampleGate(s.Edges, tpSampleVerdict{
		Total: 200, Complete: true, Confidence: "settled",
		Partial: true, MissingBatches: 3, Known: true,
	})
	if !s.Gated {
		t.Fatal("a partial store read was not gated")
	}
	out := renderTPPerVisorPanelANSI(s)
	if !strings.Contains(out, "histogram withheld") {
		t.Error("the panel did not say the histogram was withheld")
	}
	if !strings.Contains(out, "#4513") {
		t.Error("the gate reason does not cite the oscillation issue")
	}
	if !strings.Contains(out, "#4542") {
		t.Error("a partial read must name the flag it came from")
	}
	if strings.Contains(out, "█") {
		t.Error("a withheld sample still drew bars; the shape is exactly what must not be shown")
	}
}

// The published completeness stamp (#4526) is the other refusal: a
// refilling index reads as visors holding fewer transports than they hold.
func TestPerVisorPanelWithholdsARefillingSample(t *testing.T) {
	s := bucketTransportCounts(evenCounts(50, 2))
	s.Gated, s.GateWhy = tpSampleGate(s.Edges, tpSampleVerdict{
		Total: 50, Complete: false, Confidence: "refilling", Known: true,
	})
	if !s.Gated {
		t.Fatal("an incomplete sample was not gated")
	}
	out := renderTPPerVisorPanelANSI(s)
	if !strings.Contains(out, "refilling") || !strings.Contains(out, "#4513") {
		t.Error("the refilling verdict or its issue reference is missing from the notice")
	}
}

// Self-consistency: every transport contributes exactly two edges. When
// the per-key index and the aggregate disagree, the two were read from
// different states of the same index — the oscillation, caught in the act.
func TestPerVisorPanelWithholdsWhenEdgesDisagreeWithTheAggregate(t *testing.T) {
	s := bucketTransportCounts(evenCounts(1000, 4)) // 4,000 edges
	// The aggregate claims 3,000 transports = 6,000 edges. A third apart.
	s.Gated, s.GateWhy = tpSampleGate(s.Edges, tpSampleVerdict{
		Total: 3000, Complete: true, Confidence: "settled", Known: true,
	})
	if !s.Gated {
		t.Fatal("an index disagreeing with the aggregate by a third was drawn as data")
	}
	if !strings.Contains(s.GateWhy, "#4513") {
		t.Error("the skew reason does not cite the oscillation issue")
	}
	// Within tolerance the same sample must draw: the gate is a guard, not
	// a permanent off switch.
	s2 := bucketTransportCounts(evenCounts(1000, 4))
	s2.Gated, s2.GateWhy = tpSampleGate(s2.Edges, vouched(s2.Edges))
	if s2.Gated {
		t.Fatalf("a self-consistent sample was refused: %s", s2.GateWhy)
	}
}

// No aggregate to check against is not a pass. An unjudged sample is
// exactly the one #4513 makes untrustworthy.
func TestPerVisorPanelWithholdsAnUnjudgedSample(t *testing.T) {
	gated, why := tpSampleGate(400, tpSampleVerdict{})
	if !gated {
		t.Fatal("a sample with no aggregate to check against was drawn")
	}
	if !strings.Contains(why, "#4513") {
		t.Error("the reason does not cite the oscillation issue")
	}
}

// A vouched sample draws, with the figures that make the shape readable.
func TestPerVisorPanelDrawsAVouchedSample(t *testing.T) {
	s := bucketTransportCounts(evenCounts(300, 3))
	s.Src = "source: HTTP over dmsg /all-transports/per-key-stats"
	s.Gated, s.GateWhy = tpSampleGate(s.Edges, vouched(s.Edges))
	out := renderTPPerVisorPanelANSI(s)

	if strings.Contains(out, "withheld") {
		t.Fatalf("a vouched sample was withheld: %s", s.GateWhy)
	}
	if !strings.Contains(out, "█") {
		t.Error("no bars were drawn")
	}
	for _, want := range []string{"TRANSPORTS PER VISOR", "median 3", "300 visors", "\x1b["} {
		if !strings.Contains(out, want) {
			t.Errorf("rendering is missing %q", want)
		}
	}
	if !strings.Contains(out, "not transports") {
		t.Error("the panel does not say the bars count visors")
	}
}

// A failed fetch is named, never rendered as an empty or zeroed panel.
func TestPerVisorPanelNamesFailures(t *testing.T) {
	out := renderTPPerVisorPanelANSI(tpPerVisorStats{Err: "request failed: EOF"})
	if !strings.Contains(out, "unavailable") || !strings.Contains(out, "EOF") {
		t.Error("a failed fetch was not reported")
	}
	out = renderTPPerVisorPanelANSI(tpPerVisorStats{})
	if !strings.Contains(out, "TRANSPORTS PER VISOR") || !strings.Contains(out, "unavailable") {
		t.Error("an empty panel disappeared instead of reporting itself absent")
	}
}

// The gate reasons are sentences and the panel is 78 columns. An
// unwrapped sentence runs off the rule in the 80-column target.
func TestGateReasonsAreWrappedToThePanel(t *testing.T) {
	s := bucketTransportCounts(evenCounts(10, 1))
	s.Gated, s.GateWhy = tpSampleGate(s.Edges, tpSampleVerdict{
		Total: 500, Complete: true, Confidence: "settled", Known: true,
	})
	for _, line := range strings.Split(renderTPPerVisorPanelANSI(s), "\n") {
		// Columns, not bytes: the rules are box-drawing runes.
		plain := []rune(stripANSI(line))
		if len(plain) > tuiWidth+2 {
			t.Errorf("line runs to %d columns: %q", len(plain), string(plain))
		}
	}
}

// stripANSI removes SGR sequences so a rendered line can be measured.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
