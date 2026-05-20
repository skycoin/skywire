package rpcgrpc

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// TestNormalizePingTreeRequest pins the "0 = default" fallback
// table. Without these, a zero-valued request would either run
// forever (zero timeout) or hammer the visor with zero-concurrency
// (no parallelism) or zero-tries (no measurements). Concrete defaults
// keep an unconfigured call usable.
func TestNormalizePingTreeRequest(t *testing.T) {
	t.Run("zero request falls back to all defaults", func(t *testing.T) {
		req := &PingTreeRequest{}
		c := normalizePingTreeRequest(req)
		if c.Tries != defaultPingTreeTries {
			t.Errorf("Tries default = %d, want %d", c.Tries, defaultPingTreeTries)
		}
		if c.PacketSizeKb != defaultPingTreePacketSizeKb {
			t.Errorf("PacketSizeKb default = %d, want %d", c.PacketSizeKb, defaultPingTreePacketSizeKb)
		}
		if c.PingTimeout != defaultPingTreePingTimeout {
			t.Errorf("PingTimeout default = %v, want %v", c.PingTimeout, defaultPingTreePingTimeout)
		}
		if c.SetupTimeout != defaultPingTreeSetupTimeout {
			t.Errorf("SetupTimeout default = %v, want %v", c.SetupTimeout, defaultPingTreeSetupTimeout)
		}
		if c.Concurrency != defaultPingTreeConcurrency {
			t.Errorf("Concurrency default = %d, want %d", c.Concurrency, defaultPingTreeConcurrency)
		}
	})

	t.Run("explicit values pass through", func(t *testing.T) {
		req := &PingTreeRequest{
			MaxLevel:            5,
			Hops:                3,
			Tries:               10,
			PacketSizeKb:        64,
			PingTimeoutNs:       (5 * time.Second).Nanoseconds(),
			SetupTimeoutNs:      (15 * time.Second).Nanoseconds(),
			Concurrency:         32,
			OnlineOnly:          true,
			MinVersion:          "v1.3.0",
			UseTransportLatency: true,
			DmsgOnly:            true,
			DmsgPreCheck:        true,
			Retries:             2,
			DryRun:              true,
		}
		c := normalizePingTreeRequest(req)
		if c.MaxLevel != 5 || c.Hops != 3 || c.Tries != 10 || c.PacketSizeKb != 64 {
			t.Errorf("explicit values lost: %+v", c)
		}
		if c.PingTimeout != 5*time.Second || c.SetupTimeout != 15*time.Second {
			t.Errorf("timeout values lost: ping=%v setup=%v", c.PingTimeout, c.SetupTimeout)
		}
		if c.Concurrency != 32 {
			t.Errorf("Concurrency = %d, want 32", c.Concurrency)
		}
		if !c.OnlineOnly || c.MinVersion != "v1.3.0" || !c.UseTransportLatency ||
			!c.DmsgOnly || !c.DmsgPreCheck || c.Retries != 2 || !c.DryRun {
			t.Errorf("bool/string passthrough lost: %+v", c)
		}
	})

	t.Run("negative values fall back to defaults", func(t *testing.T) {
		// Proto fields are int32 so negative-encoded values are
		// theoretically possible; treat them as "default" rather than
		// propagate nonsense into the run loop.
		req := &PingTreeRequest{
			Tries:        -5,
			PacketSizeKb: -100,
			Concurrency:  -1,
		}
		c := normalizePingTreeRequest(req)
		if c.Tries != defaultPingTreeTries {
			t.Errorf("negative Tries should default, got %d", c.Tries)
		}
		if c.PacketSizeKb != defaultPingTreePacketSizeKb {
			t.Errorf("negative PacketSizeKb should default, got %d", c.PacketSizeKb)
		}
		if c.Concurrency != defaultPingTreeConcurrency {
			t.Errorf("negative Concurrency should default, got %d", c.Concurrency)
		}
	})
}

// TestAggregatePingSamples pins the stats math (avg/p50/p99/jitter).
// Synth's directive specifically calls out jitter as a measurement
// target, so the formula choice (population stddev) is locked in
// here against accidental changes.
func TestAggregatePingSamples(t *testing.T) {
	t.Run("empty samples yields zeros", func(t *testing.T) {
		avg, p50, p99, jitter := aggregatePingSamples(nil)
		if avg != 0 || p50 != 0 || p99 != 0 || jitter != 0 {
			t.Errorf("empty: got avg=%d p50=%d p99=%d jitter=%d, want all 0",
				avg, p50, p99, jitter)
		}
	})

	t.Run("single sample: avg=p50=p99, jitter=0", func(t *testing.T) {
		// stddev undefined for n<2; the handler returns 0 by
		// definition (documented on PingTreeResult.jitter_ns).
		avg, p50, p99, jitter := aggregatePingSamples([]float64{500})
		if avg != 500 || p50 != 500 || p99 != 500 {
			t.Errorf("single: got avg=%d p50=%d p99=%d, want all 500", avg, p50, p99)
		}
		if jitter != 0 {
			t.Errorf("single: jitter = %d, want 0 (undefined for n<2)", jitter)
		}
	})

	t.Run("uniform samples have zero jitter", func(t *testing.T) {
		// Population stddev of an all-same series is 0.
		_, _, _, jitter := aggregatePingSamples([]float64{100, 100, 100, 100, 100})
		if jitter != 0 {
			t.Errorf("uniform: jitter = %d, want 0", jitter)
		}
	})

	t.Run("five-sample series exercises percentile + stddev", func(t *testing.T) {
		// Series: 100, 200, 300, 400, 500
		// avg = 300
		// sorted [100,200,300,400,500] → p50 = idx 2 (300), p99 = idx 4 (500)
		// Population stddev: sqrt(sum((x - mean)^2) / N)
		//   sum of squares = 40000 + 10000 + 0 + 10000 + 40000 = 100000
		//   stddev = sqrt(100000 / 5) = sqrt(20000) ≈ 141.42
		avg, p50, p99, jitter := aggregatePingSamples([]float64{100, 200, 300, 400, 500})
		if avg != 300 {
			t.Errorf("avg = %d, want 300", avg)
		}
		if p50 != 300 {
			t.Errorf("p50 = %d, want 300", p50)
		}
		if p99 != 500 {
			t.Errorf("p99 = %d, want 500", p99)
		}
		// Jitter as int64 nanoseconds: sqrt(20000) ≈ 141, give it
		// ±2 slack for the int conversion.
		if jitter < 139 || jitter > 143 {
			t.Errorf("jitter = %d, want ~141 (population stddev of the series)", jitter)
		}
	})

	t.Run("unsorted input produces same result as sorted", func(t *testing.T) {
		a1, p501, p991, j1 := aggregatePingSamples([]float64{300, 100, 500, 200, 400})
		a2, p502, p992, j2 := aggregatePingSamples([]float64{100, 200, 300, 400, 500})
		if a1 != a2 || p501 != p502 || p991 != p992 || j1 != j2 {
			t.Errorf("unsorted gave different result: (%d,%d,%d,%d) vs (%d,%d,%d,%d)",
				a1, p501, p991, j1, a2, p502, p992, j2)
		}
	})
}

// TestBuildPingTreeAdjacency pins the adjacency-map structure.
// Operator-visible behavior: every transport produces TWO edges
// (one per direction) so BFS can walk in either direction. Self-
// loops (edge a == edge b) only contribute one edge to avoid
// double-counting in the BFS frontier.
func TestBuildPingTreeAdjacency(t *testing.T) {
	pk := func(b byte) cipher.PubKey {
		var p cipher.PubKey
		p[0] = 0x02
		p[1] = b
		return p
	}
	pkA, pkB, pkC := pk(0xa1), pk(0xb1), pk(0xc1)
	t1 := uuid.New()
	t2 := uuid.New()

	entries := []*transport.Entry{
		{ID: t1, Edges: [2]cipher.PubKey{pkA, pkB}, Type: tptypes.STCPR},
		{ID: t2, Edges: [2]cipher.PubKey{pkB, pkC}, Type: tptypes.SUDPH},
	}
	adj := buildPingTreeAdjacency(entries)

	a, b, c := pkA.String(), pkB.String(), pkC.String()

	if len(adj[a]) != 1 || adj[a][0].remotePK != b {
		t.Errorf("a's neighbors = %+v, want [%s]", adj[a], b)
	}
	if len(adj[b]) != 2 {
		t.Errorf("b's neighbors = %+v, want 2 entries", adj[b])
	}
	if len(adj[c]) != 1 || adj[c][0].remotePK != b {
		t.Errorf("c's neighbors = %+v, want [%s]", adj[c], b)
	}
}

// TestCandidatesFromAdjacency pins the dedup-by-remote-PK behavior.
// If a peer is reachable via multiple transports we only emit one
// candidate (the BFS pings the peer, not the transport — the
// route-finder chooses transport).
func TestCandidatesFromAdjacency(t *testing.T) {
	pkLocal := "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c"
	pkPeer1 := "02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36"
	pkPeer2 := "03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c"

	// Local has 3 transports: stcpr+sudph to peer1, stcpr to peer2.
	adj := map[string][]pingTreeAdjacencyEntry{
		pkLocal: {
			{tpID: "tp-stcpr-1", tpType: "stcpr", remotePK: pkPeer1},
			{tpID: "tp-sudph-1", tpType: "sudph", remotePK: pkPeer1},
			{tpID: "tp-stcpr-2", tpType: "stcpr", remotePK: pkPeer2},
		},
	}
	visited := map[string]bool{pkLocal: true}
	out := candidatesFromAdjacency(adj, pkLocal, pkLocal, 1, visited)
	if len(out) != 2 {
		t.Fatalf("got %d candidates, want 2 (dedup by remote PK)", len(out))
	}
	// Verify both peers are present.
	got := map[string]bool{}
	for _, c := range out {
		got[c.remotePK] = true
	}
	if !got[pkPeer1] || !got[pkPeer2] {
		t.Errorf("missing peer: got %v", got)
	}
	// Verify visited is updated so subsequent calls skip them.
	out2 := candidatesFromAdjacency(adj, pkLocal, pkLocal, 1, visited)
	if len(out2) != 0 {
		t.Errorf("second call should produce 0 candidates (all visited), got %d", len(out2))
	}
}

// TestPingTreeTotalsBumpInFlight pins peak-tracking. Operator can't
// tune concurrency without knowing what peak was actually hit, so the
// RunDone.PeakInFlight metric needs to be correct under contention.
func TestPingTreeTotalsBumpInFlight(t *testing.T) {
	tot := &pingTreeTotals{}
	tot.bumpInFlight(1)
	tot.bumpInFlight(1)
	tot.bumpInFlight(1)
	tot.bumpInFlight(-1)
	tot.bumpInFlight(1)
	tot.bumpInFlight(-1)
	tot.bumpInFlight(-1)
	tot.bumpInFlight(-1)

	if tot.peakInFlight != 3 {
		t.Errorf("peak = %d, want 3 (max of 1,2,3,2,3,2,1,0)", tot.peakInFlight)
	}
	if tot.currentInFlight != 0 {
		t.Errorf("current = %d, want 0 (balanced bumps)", tot.currentInFlight)
	}
}
