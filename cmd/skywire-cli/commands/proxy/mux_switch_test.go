// Package skysocksc cmd/skywire-cli/commands/proxy/mux_switch_test.go
package skysocksc

import "testing"

// TestPrimaryLegTpID confirms `switch` retires the lowest-Index leg (the
// primary) regardless of slice order, and errors cleanly on an empty group.
func TestPrimaryLegTpID(t *testing.T) {
	const primaryID = "11111111-1111-1111-1111-111111111111"
	rg := muxRouteGroupInfo{Legs: []muxLegInfo{
		{Index: 2, TransportID: "22222222-2222-2222-2222-222222222222"},
		{Index: 0, TransportID: primaryID},
		{Index: 1, TransportID: "33333333-3333-3333-3333-333333333333"},
	}}
	got, err := primaryLegTpID(rg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.String() != primaryID {
		t.Fatalf("primary = %s, want %s (lowest Index)", got, primaryID)
	}

	if _, err := primaryLegTpID(muxRouteGroupInfo{}); err == nil {
		t.Fatal("expected error for a route group with no legs")
	}

	bad := muxRouteGroupInfo{Legs: []muxLegInfo{{Index: 0, TransportID: "not-a-uuid"}}}
	if _, err := primaryLegTpID(bad); err == nil {
		t.Fatal("expected parse error for a non-uuid transport id")
	}
}
