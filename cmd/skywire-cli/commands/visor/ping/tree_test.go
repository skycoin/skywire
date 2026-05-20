package ping

import (
	"strings"
	"testing"
	"time"
)

// TestEntryCategory pins the sort-key the renderer uses to group
// rows within a level: successes float to the top, pending in the
// middle, failures at the bottom. If this ordering changes the TUI
// behavior shifts visibly, so the contract is locked here.
func TestEntryCategory(t *testing.T) {
	cases := []struct {
		name string
		in   treeEntry
		want int
	}{
		{"discovered but not yet pinged → pending", treeEntry{pinged: false}, 1},
		{"pinged + ok → succeeded", treeEntry{pinged: true, failed: false}, 0},
		{"pinged + failed", treeEntry{pinged: true, failed: true}, 2},
		{"pinged + canceled+failed", treeEntry{pinged: true, failed: true, canceled: true}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := entryCategory(&c.in); got != c.want {
				t.Errorf("entryCategory = %d, want %d", got, c.want)
			}
		})
	}
}

// TestFormatEntryLine spot-checks the per-row formatter for the
// three visible states. Format is consumer-visible TUI output so
// changes here change what operators see; the test pins the broad
// shape (prefix glyph, presence of source-tag/latency fields)
// without being so strict that minor styling tweaks fail it.
func TestFormatEntryLine(t *testing.T) {
	now := time.Now()
	pk := "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c"

	t.Run("pending entry shows pending marker", func(t *testing.T) {
		line := formatEntryLine(&treeEntry{
			tpID:     "tp-1",
			tpType:   "stcpr",
			remotePK: pk,
			level:    1,
			ts:       now,
		})
		if !strings.Contains(line, "·") {
			t.Errorf("pending line missing · prefix: %q", line)
		}
		if !strings.Contains(line, "pending") {
			t.Errorf("pending line missing 'pending' label: %q", line)
		}
	})

	t.Run("succeeded live_ping shows live tag + percentile fields", func(t *testing.T) {
		line := formatEntryLine(&treeEntry{
			tpID:           "tp-1",
			tpType:         "stcpr",
			remotePK:       pk,
			level:          1,
			pinged:         true,
			latencySource:  "live_ping",
			pingAvgMs:      123.4,
			pingP50Ms:      120.0,
			pingP99Ms:      145.0,
			jitterMs:       4.5,
			sampleCount:    5,
			setupLatencyMs: 87.0,
			ts:             now,
		})
		if !strings.Contains(line, "✓") {
			t.Errorf("success line missing ✓ prefix: %q", line)
		}
		if !strings.Contains(line, "live") {
			t.Errorf("live_ping should be tagged 'live': %q", line)
		}
		if !strings.Contains(line, "p50") {
			t.Errorf("multi-sample success line should show p50: %q", line)
		}
	})

	t.Run("succeeded transport_summary shows cache tag, no percentiles", func(t *testing.T) {
		line := formatEntryLine(&treeEntry{
			tpID:          "tp-1",
			tpType:        "stcpr",
			remotePK:      pk,
			level:         1,
			pinged:        true,
			latencySource: "transport_summary",
			pingAvgMs:     265.7,
			sampleCount:   1,
			ts:            now,
		})
		if !strings.Contains(line, "cache") {
			t.Errorf("transport_summary should be tagged 'cache': %q", line)
		}
		if strings.Contains(line, "p50") {
			t.Errorf("single-sample line should NOT show p50 (only one value to report): %q", line)
		}
	})

	t.Run("failed entry shows ✗ prefix and error text", func(t *testing.T) {
		line := formatEntryLine(&treeEntry{
			tpID:     "tp-1",
			tpType:   "stcpr",
			remotePK: pk,
			level:    2,
			pinged:   true,
			failed:   true,
			setupErr: "setup timeout after 10s",
			ts:       now,
		})
		if !strings.Contains(line, "✗") {
			t.Errorf("failed line missing ✗ prefix: %q", line)
		}
		if !strings.Contains(line, "setup timeout") {
			t.Errorf("failed line missing setup_err text: %q", line)
		}
	})

	t.Run("canceled+failed uses ⊘ prefix", func(t *testing.T) {
		line := formatEntryLine(&treeEntry{
			tpID:     "tp-1",
			tpType:   "stcpr",
			remotePK: pk,
			level:    1,
			pinged:   true,
			failed:   true,
			canceled: true,
			pingErr:  "ctx canceled",
			ts:       now,
		})
		if !strings.Contains(line, "⊘") {
			t.Errorf("canceled line should use ⊘ prefix to distinguish from genuine failure: %q", line)
		}
	})

	t.Run("long error message truncates to keep the row one-line", func(t *testing.T) {
		longErr := strings.Repeat("x", 200)
		line := formatEntryLine(&treeEntry{
			tpID:     "tp-1",
			tpType:   "stcpr",
			remotePK: pk,
			level:    2,
			pinged:   true,
			failed:   true,
			setupErr: longErr,
			ts:       now,
		})
		if len(line) > 200 {
			// The truncation isn't byte-exact (lipgloss styling and
			// prefix add overhead), but a 200-char error string in
			// the source should produce a line well under 200 chars.
			t.Errorf("long error should be truncated; line length = %d", len(line))
		}
		if !strings.Contains(line, "...") {
			t.Errorf("truncated line should end with ... marker: %q", line)
		}
	})
}
