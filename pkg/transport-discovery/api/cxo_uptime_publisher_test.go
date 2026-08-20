// Package api pkg/transport-discovery/api/cxo_uptime_publisher_test.go c4-net-discovery
package api

import (
	"encoding/json"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

// renderBitmap is the reference inverse of timelineStringToBitmap — the exact
// logic the reader (pkg/visor) applies. Kept local so this test verifies the
// publisher's packing against an independent renderer.
func renderBitmap(bm []byte) string {
	out := make([]byte, 288)
	for i := range out {
		if i/8 < len(bm) && bm[i/8]&(1<<uint(7-i%8)) != 0 {
			out[i] = '.'
		} else {
			out[i] = ' '
		}
	}
	return string(out)
}

func TestTimelineStringToBitmapRoundTrip(t *testing.T) {
	// A representative 288-char line: some set slots (incl. bit 0 and bit 7,
	// which live in the same byte at MSB/LSB), gaps, and a set slot at the end.
	line := make([]byte, 288)
	for i := range line {
		line[i] = ' '
	}
	set := []int{0, 7, 8, 100, 200, 287}
	for _, s := range set {
		line[s] = '.'
	}
	bm := timelineStringToBitmap(string(line))
	if len(bm) != 36 {
		t.Fatalf("bitmap length = %d, want 36", len(bm))
	}
	if got := renderBitmap(bm); got != string(line) {
		t.Fatalf("round-trip mismatch:\n got %q\nwant %q", got, string(line))
	}
}

func TestToCompactVisorSummaryPreservesFields(t *testing.T) {
	var pk cipher.PubKey
	s := store.VisorSummary{
		PK:      pk,
		Online:  true,
		Version: "v1.3.99",
		Daily:   map[string]string{"2026-08-19": "1.0"},
		Timeline: map[string]string{
			"2026-08-19": func() string {
				b := make([]byte, 288)
				for i := range b {
					b[i] = ' '
				}
				b[5] = '.'
				return string(b)
			}(),
		},
	}
	c := toCompactVisorSummary(s)
	if c.Online != s.Online || c.Version != s.Version {
		t.Fatalf("scalar fields not preserved: %+v", c)
	}
	if c.Daily["2026-08-19"] != "1.0" {
		t.Fatalf("daily not preserved: %+v", c.Daily)
	}
	bm := c.Timeline["2026-08-19"]
	if len(bm) != 36 || !(bm[0] == 0x04) { // slot 5 -> bit (7-5)=2 in byte 0 => 0b00000100
		t.Fatalf("timeline bitmap wrong: %x", bm)
	}
	// The whole compact summary must JSON round-trip (base64 for the bitmap).
	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back compactVisorSummary
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if renderBitmap(back.Timeline["2026-08-19"]) != s.Timeline["2026-08-19"] {
		t.Fatal("timeline did not survive JSON round-trip")
	}
}
