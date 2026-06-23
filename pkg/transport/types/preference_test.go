package tptypes

import (
	"sort"
	"testing"
)

// TestDefaultPreferenceOrder pins the built-in priority order and confirms QUIC
// (previously absent → sorted below DMSG) now ranks above SUDPH, and every direct
// type outranks DMSG.
func TestDefaultPreferenceOrder(t *testing.T) {
	SetPreferenceOrder(nil) // ensure the built-in default is active

	want := []Type{STCPR, QUIC, SUDPH, STCP, WEBRTC, WS, WT, DMSG}
	for i := 1; i < len(want); i++ {
		if TypePreference(want[i-1]) >= TypePreference(want[i]) {
			t.Fatalf("order broken: %s (%d) should rank before %s (%d)",
				want[i-1], TypePreference(want[i-1]), want[i], TypePreference(want[i]))
		}
	}

	// QUIC must beat DMSG (the bug: it used to sort last, below DMSG).
	if TypePreference(QUIC) >= TypePreference(DMSG) {
		t.Fatalf("QUIC (%d) must outrank DMSG (%d)", TypePreference(QUIC), TypePreference(DMSG))
	}

	// An unknown type sorts after everything listed.
	if TypePreference(Type("bogus")) != len(want) {
		t.Fatalf("unknown type should sort last, got %d", TypePreference(Type("bogus")))
	}
}

// TestParsePreferenceOrder confirms every known type (canonical "squic" included)
// parses, and unknown strings are dropped.
func TestParsePreferenceOrder(t *testing.T) {
	in := []string{"stcpr", "squic", "sudph", "stcp", "webrtc", "ws", "wt", "dmsg", "nope"}
	got := ParsePreferenceOrder(in)
	want := []Type{STCPR, QUIC, SUDPH, STCP, WEBRTC, WS, WT, DMSG}
	if len(got) != len(want) {
		t.Fatalf("parsed %d types, want %d (unknown 'nope' should be dropped): %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parsed[%d]=%s, want %s", i, got[i], want[i])
		}
	}
}

// TestQUICLegacyAlias confirms the canonical QUIC wire name is "squic" and the
// pre-rename "quic" still parses + normalizes for back-compat.
func TestQUICLegacyAlias(t *testing.T) {
	if QUIC != "squic" {
		t.Fatalf("QUIC canonical wire name = %q, want squic", QUIC)
	}
	if NormalizeType(QUICLegacy) != QUIC {
		t.Fatalf(`NormalizeType("quic") = %q, want squic`, NormalizeType(QUICLegacy))
	}
	if NormalizeType(QUIC) != QUIC {
		t.Fatalf("NormalizeType is not idempotent on the canonical name")
	}
	// Both names parse to QUIC in a preference list.
	if got := ParsePreferenceOrder([]string{"quic"}); len(got) != 1 || got[0] != QUIC {
		t.Fatalf(`ParsePreferenceOrder(["quic"]) = %v, want [squic]`, got)
	}
}

// TestSetPreferenceOrderSortsTransports verifies a configured override actually
// reorders a slice the way the router's dial logic relies on.
func TestSetPreferenceOrderSortsTransports(t *testing.T) {
	defer SetPreferenceOrder(nil)
	SetPreferenceOrder([]Type{QUIC, STCPR, DMSG}) // QUIC first now

	tps := []Type{DMSG, STCPR, QUIC}
	sort.Slice(tps, func(i, j int) bool { return TypePreference(tps[i]) < TypePreference(tps[j]) })
	if tps[0] != QUIC || tps[1] != STCPR || tps[2] != DMSG {
		t.Fatalf("custom order not applied: %v", tps)
	}
}
