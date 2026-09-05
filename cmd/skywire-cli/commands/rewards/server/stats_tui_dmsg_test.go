// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_dmsg_test.go c5-reward-server
package clirewardsserver

import (
	"strings"
	"testing"
)

// availableSessions is capacity REMAINING. A server at zero is FULL and gets
// excluded from available_servers by design — reading it as load inverts the
// meaning and makes the busiest server look like the emptiest. That inversion
// really happened: a saturated server was reported as having no clients.
func TestDmsgPanelMarksAFullServerAsFullNotIdle(t *testing.T) {
	out := renderDmsgPanelANSI(dmsgStats{
		Servers: []dmsgServerRow{
			{PK: strings.Repeat("a", 66), Address: "45.79.124.73:30082", Available: 0, Listed: false},
			{PK: strings.Repeat("b", 66), Address: "143.42.59.213:30088", Available: 1259, Listed: true},
		},
		Entries: 931,
	})

	if !strings.Contains(out, "FULL") {
		t.Error("a server at zero spare capacity was not marked FULL")
	}
	if !strings.Contains(out, "spare slots, not load") {
		t.Error("the panel does not say the figures are spare capacity rather than load")
	}
	if !strings.Contains(out, "1 at capacity") {
		t.Error("the at-capacity count is wrong or missing")
	}
}

// Public keys are never truncated: an operator matching a server against their
// own config needs the whole key.
func TestDmsgPanelPrintsFullPublicKeys(t *testing.T) {
	pk := "02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"
	out := renderDmsgPanelANSI(dmsgStats{
		Servers: []dmsgServerRow{{PK: pk, Address: "45.79.124.73:30082", Available: 997, Listed: true}},
	})
	if !strings.Contains(out, pk) {
		t.Error("the full public key is not present")
	}
}

// Ordering belongs to the gatherer (fullest first), not the renderer. Assert
// the renderer draws rows in exactly the order it is handed, so there is only
// one place that decides what "first" means.
func TestDmsgPanelPreservesGivenOrder(t *testing.T) {
	out := renderDmsgPanelANSI(dmsgStats{Servers: []dmsgServerRow{
		{PK: strings.Repeat("b", 66), Address: "roomy:1", Available: 1200, Listed: true},
		{PK: strings.Repeat("a", 66), Address: "full:2", Available: 0, Listed: false},
	}})

	roomy, full := strings.Index(out, "roomy:1"), strings.Index(out, "full:2")
	if roomy < 0 || full < 0 {
		t.Fatal("a server row is missing from the rendering")
	}
	if roomy > full {
		t.Error("the renderer reordered rows; sorting must stay in gatherDmsgStats")
	}
}

// A failed fetch is named, never rendered as an empty or zeroed panel.
func TestDmsgPanelNamesFailures(t *testing.T) {
	out := renderDmsgPanelANSI(dmsgStats{ServersErr: "request failed: EOF"})
	if !strings.Contains(out, "unavailable") || !strings.Contains(out, "EOF") {
		t.Error("a failed server fetch was not reported")
	}
	// Servers fine, entries dead: the working half must still draw.
	out = renderDmsgPanelANSI(dmsgStats{
		Servers:    []dmsgServerRow{{PK: "x", Address: "a:1", Available: 5, Listed: true}},
		EntriesErr: "status 502",
	})
	if !strings.Contains(out, "DMSG SERVERS") {
		t.Error("a working section was suppressed by an unrelated failure")
	}
	if !strings.Contains(out, "502") {
		t.Error("the entries failure was not reported")
	}
}

// No data and no error must still say something rather than vanish.
func TestDmsgPanelNeverSilentlyEmpty(t *testing.T) {
	out := renderDmsgPanelANSI(dmsgStats{})
	if !strings.Contains(out, "DMSG SERVERS") || !strings.Contains(out, "unavailable") {
		t.Error("an empty panel disappeared instead of reporting itself absent")
	}
}
