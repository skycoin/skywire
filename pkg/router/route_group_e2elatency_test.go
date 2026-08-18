//go:build !tinygo || (js && wasm)

package router

import (
	"testing"

	"github.com/google/uuid"
)

// TestSnapshotLegsPrefersEndToEndLatency: when a leg has a measured end-to-end
// latency (from the liveness pong), snapshotLegs reports THAT as LatencyMs — the
// whole-route quality signal a policy's slowest-leg eviction should judge — and
// falls back to the first-hop transport RTT only until the first pong lands.
func TestSnapshotLegsPrefersEndToEndLatency(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 2)

	// Both legs report a slow first-hop transport RTT.
	mts[0].SetLatency(500)
	mts[1].SetLatency(500)

	// Leg 0 has a measured (lower) end-to-end latency; leg 1 has none yet.
	rg.legLivenessMu.Lock()
	rg.legE2ELatency[mts[0].Entry.ID] = 80
	rg.legLivenessMu.Unlock()

	rg.mu.Lock()
	legs := rg.snapshotLegs()
	rg.mu.Unlock()

	if len(legs) != 2 {
		t.Fatalf("got %d legs, want 2", len(legs))
	}
	if legs[0].LatencyMs != 80 {
		t.Errorf("leg 0 LatencyMs = %d, want 80 (measured end-to-end)", legs[0].LatencyMs)
	}
	if legs[1].LatencyMs != 500 {
		t.Errorf("leg 1 LatencyMs = %d, want 500 (first-hop RTT fallback)", legs[1].LatencyMs)
	}
}

// TestLegE2ELatencyPrunedForDepartedTransport: snapshotLegs drops per-leg
// latency entries for transport IDs no longer in the group, keeping the map
// bounded across rotation.
func TestLegE2ELatencyPrunedForDepartedTransport(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 1)

	stale := uuid.New()
	rg.legLivenessMu.Lock()
	rg.legE2ELatency[mts[0].Entry.ID] = 40 // live leg
	rg.legE2ELatency[stale] = 999          // departed transport
	rg.legLivenessMu.Unlock()

	rg.mu.Lock()
	_ = rg.snapshotLegs()
	rg.mu.Unlock()

	rg.legLivenessMu.Lock()
	_, staleKept := rg.legE2ELatency[stale]
	_, liveKept := rg.legE2ELatency[mts[0].Entry.ID]
	rg.legLivenessMu.Unlock()

	if staleKept {
		t.Error("stale (departed) transport latency should have been pruned")
	}
	if !liveKept {
		t.Error("live leg latency must be kept")
	}
}
