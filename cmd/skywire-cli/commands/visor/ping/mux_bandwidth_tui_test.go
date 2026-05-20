package ping

import (
	"strings"
	"testing"
	"time"
)

// TestSparkline pins the sparkline rendering contract:
//   - empty input → fixed-width blank string
//   - all-zero input → low-glyph filled (so the operator sees the
//     sparkline exists even before data flows)
//   - mixed values scale to the max in the window
//   - shorter-than-width input pads with trailing spaces
//   - longer-than-width input uses the trailing `width` values
func TestSparkline(t *testing.T) {
	width := 10

	if got := sparkline(nil, width); got != strings.Repeat(" ", width) {
		t.Errorf("nil input: got %q, want %d spaces", got, width)
	}

	zeros := []float64{0, 0, 0, 0, 0}
	got := sparkline(zeros, width)
	// 5 of the lowest-glyph rune + 5 spaces. Width counted in runes.
	if runeCount(got) != width {
		t.Errorf("zeros: width = %d, want %d (got %q)", runeCount(got), width, got)
	}

	mixed := []float64{1, 2, 4, 8, 0}
	got = sparkline(mixed, width)
	if runeCount(got) != width {
		t.Errorf("mixed: width = %d, want %d", runeCount(got), width)
	}
	// Largest value should produce the largest glyph (last index).
	largestIdx := strings.IndexRune(got, '█')
	if largestIdx < 0 {
		t.Errorf("mixed: expected highest glyph for max value, got %q", got)
	}

	// Longer than width — last `width` only.
	long := make([]float64, 30)
	for i := range long {
		long[i] = float64(i)
	}
	got = sparkline(long, width)
	if runeCount(got) != width {
		t.Errorf("long: width = %d, want %d", runeCount(got), width)
	}
	// Last value (29) is the max — the rightmost glyph must be the
	// highest, since the window is values[20..30) and 29 is highest.
	rs := []rune(got)
	if rs[len(rs)-1] != '█' {
		t.Errorf("long: last glyph = %q, want █ (got line %q)", rs[len(rs)-1], got)
	}
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// TestFormatBps pins the unit selection. fixed-width output is a
// hard contract — the dashboard layout relies on the column staying
// the same width across orders of magnitude.
func TestFormatBps(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "  0.00 bps"},
		{500, "500.00 bps"},
		{1500, "  1.50Kbps"},
		{1_500_000, "  1.50Mbps"},
		{1_500_000_000, "  1.50Gbps"},
	}
	for _, c := range cases {
		got := formatBps(c.in)
		if got != c.want {
			t.Errorf("formatBps(%v) = %q, want %q", c.in, got, c.want)
		}
		if len(got) != 10 {
			t.Errorf("formatBps(%v) = %q (len=%d), want fixed width 10", c.in, got, len(got))
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "     0 B"},
		{500, "   500 B"},
		{2048, "  2.00KiB"},
		{2 * 1024 * 1024, "  2.00MiB"},
		{2 * 1024 * 1024 * 1024, "  2.00GiB"},
	}
	for _, c := range cases {
		got := formatBytes(c.in)
		if got != c.want {
			t.Errorf("formatBytes(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0ns"},
		{500, "500ns"},
		{int64(2 * time.Microsecond), "   2.0µs"},
		{int64(2 * time.Millisecond), "   2.0ms"},
		{int64(2 * time.Second), "  2.00s"},
	}
	for _, c := range cases {
		got := formatDuration(c.in)
		if got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTruncate pins the ellipsis-on-overflow behavior used in tight
// route-tile rendering. Inputs shorter than maxLen pass through
// unchanged.
func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("short: got %q, want %q", got, "short")
	}
	if got := truncate("0123456789", 10); got != "0123456789" {
		t.Errorf("at-limit: got %q, want unchanged", got)
	}
	got := truncate("0123456789abcdef", 10)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("overflow: got %q, want trailing ellipsis", got)
	}
}

// TestAppendCapped pins the ring-buffer behavior used for both
// throughput and rttProbes slices.
func TestAppendCapped(t *testing.T) {
	s := []int{}
	for i := 0; i < 5; i++ {
		s = appendCapped(s, i, 3)
	}
	if len(s) != 3 {
		t.Errorf("len = %d, want 3", len(s))
	}
	// Should retain the LAST three values: [2, 3, 4]
	if s[0] != 2 || s[1] != 3 || s[2] != 4 {
		t.Errorf("contents = %v, want [2 3 4]", s)
	}
}
