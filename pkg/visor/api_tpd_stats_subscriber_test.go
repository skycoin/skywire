// Package visor pkg/visor/api_tpd_stats_subscriber_test.go c3-vis-core
package visor

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	tpdapi "github.com/skycoin/skywire/pkg/deployment/tpd/api"
)

// fakeStatsSnapshot serves canned leaves in place of a live CXO
// subscription.
type fakeStatsSnapshot struct {
	bodies map[string][]byte
	at     time.Time
}

func (f fakeStatsSnapshot) Get(_ CXOFeed, path string) ([]byte, time.Time, bool) {
	b, ok := f.bodies[path]
	if !ok {
		return nil, time.Time{}, false
	}
	return b, f.at, true
}

func TestStatsPathForKind(t *testing.T) {
	// The kinds the CLI accepts must resolve to the paths the TPD
	// publisher actually writes — the two constants are the contract
	// between the two packages.
	for kind, want := range map[string]string{
		StatsKindNetwork:  tpdapi.StatsPathNetwork,
		StatsKindVersions: tpdapi.StatsPathVersions,
	} {
		got, ok := statsPathForKind(kind)
		if !ok || got != want {
			t.Fatalf("statsPathForKind(%q) = (%q, %v), want (%q, true)", kind, got, ok, want)
		}
	}
	// Anything else is rejected before a feed is synced for a leaf that
	// cannot exist.
	for _, bad := range []string{"", "stats/network", "per-key", "with-self"} {
		if _, ok := statsPathForKind(bad); ok {
			t.Fatalf("statsPathForKind(%q) unexpectedly resolved", bad)
		}
	}
}

// TestReadStatsLeafRoundTrip is the reader half of the gzip contract:
// what the publisher Gzips, the reader Gunzips back to the exact JSON,
// completeness stamp intact.
func TestReadStatsLeafRoundTrip(t *testing.T) {
	want := tpdapi.NetworkStats{
		Total:                 10423,
		ByType:                map[string]int{"stcpr": 2405, "sudph": 3868},
		UniqueVisors:          930,
		ObservedAt:            time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
		Complete:              true,
		Confidence:            tpdapi.ConfidenceSettled,
		TrailingPeak:          10500,
		TrailingWindowSeconds: 900,
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	at := time.Date(2026, 9, 4, 12, 0, 12, 0, time.UTC)
	mgr := fakeStatsSnapshot{
		bodies: map[string][]byte{tpdapi.StatsPathNetwork: cxoutils.Gzip(raw)},
		at:     at,
	}

	body, ts, ok := readStatsLeaf(mgr, tpdapi.StatsPathNetwork)
	if !ok {
		t.Fatal("readStatsLeaf missed a present leaf")
	}
	if !ts.Equal(at) {
		t.Fatalf("last-root time = %v, want %v", ts, at)
	}
	var got tpdapi.NetworkStats
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoded body is not the published shape: %v", err)
	}
	if got.Total != want.Total || got.UniqueVisors != want.UniqueVisors {
		t.Fatalf("counts round-tripped wrong: %+v", got)
	}
	if !got.Complete || got.Confidence != tpdapi.ConfidenceSettled || got.TrailingPeak != want.TrailingPeak {
		t.Fatalf("completeness stamp lost in transit: %+v", got)
	}
	if !got.ObservedAt.Equal(want.ObservedAt) {
		t.Fatalf("observed_at = %v, want %v", got.ObservedAt, want.ObservedAt)
	}
}

// TestReadStatsLeafPassesRawThrough covers a publisher that has not
// been updated to gzip yet: Gunzip returns a non-gzip body unchanged,
// so the reader must still serve it rather than treating it as garbage.
func TestReadStatsLeafPassesRawThrough(t *testing.T) {
	raw := []byte(`{"total_transports":42,"by_type":{},"unique_visors":7}`)
	mgr := fakeStatsSnapshot{bodies: map[string][]byte{tpdapi.StatsPathVersions: raw}}
	body, _, ok := readStatsLeaf(mgr, tpdapi.StatsPathVersions)
	if !ok || string(body) != string(raw) {
		t.Fatalf("raw body did not pass through: ok=%v body=%q", ok, body)
	}
}

// TestReadStatsLeafMisses covers the miss paths the FetchCXO case turns
// into a cache-miss reason rather than an error.
func TestReadStatsLeafMisses(t *testing.T) {
	mgr := fakeStatsSnapshot{bodies: map[string][]byte{
		tpdapi.StatsPathNetwork: nil, // present but empty
	}}
	if _, _, ok := readStatsLeaf(mgr, tpdapi.StatsPathNetwork); ok {
		t.Fatal("an empty leaf must read as a miss")
	}
	if _, _, ok := readStatsLeaf(mgr, tpdapi.StatsPathVersions); ok {
		t.Fatal("an absent leaf must read as a miss")
	}
}

// TestReadStatsLeafCopiesBody guards against handing the RPC layer a
// slice the snapshot still owns — Get lends its bytes, and Gunzip of a
// raw body returns that same slice.
func TestReadStatsLeafCopiesBody(t *testing.T) {
	raw := []byte(`{"versions":{"v1.3.93":591},"visors":591}`)
	stored := append([]byte(nil), raw...)
	mgr := fakeStatsSnapshot{bodies: map[string][]byte{tpdapi.StatsPathVersions: stored}}
	body, _, ok := readStatsLeaf(mgr, tpdapi.StatsPathVersions)
	if !ok {
		t.Fatal("unexpected miss")
	}
	stored[0] = 'X'
	if string(body) != string(raw) {
		t.Fatalf("returned body aliases the snapshot: %q", body)
	}
}
