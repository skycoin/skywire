package cxoaggregator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestParseCurrentTransportPath(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		path string
		want bool
	}{
		{"transports/" + id.String() + "/current", true},
		{"transports/" + id.String() + "/2026-04-27", false}, // daily rollup, not "current"
		{"transports//current", false},                       // empty UUID segment
		{"tiers/dmsg/current", false},                        // wrong prefix
		{"transports/not-a-uuid/current", false},
		{"transports/" + id.String() + "/current/extra", false}, // extra segment
		{"", false},
	}
	for _, c := range cases {
		gotID, ok := parseCurrentTransportPath(c.path)
		if ok != c.want {
			t.Errorf("parseCurrentTransportPath(%q) ok = %v, want %v", c.path, ok, c.want)
			continue
		}
		if c.want && gotID != id {
			t.Errorf("parseCurrentTransportPath(%q) id = %v, want %v", c.path, gotID, id)
		}
	}
}

func TestParseTransportTimelinePath(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		path     string
		want     bool
		wantDate string
	}{
		{"transports/" + id.String() + "/2026-05-04/timeline", true, "2026-05-04"},
		{"transports/" + id.String() + "/current", false, ""},
		{"transports/" + id.String() + "/2026-05-04", false, ""},
		{"transports/" + id.String() + "/2026/05/04/timeline", false, ""},
		{"transports/not-a-uuid/2026-05-04/timeline", false, ""},
		{"transports/" + id.String() + "/26-05-04/timeline", false, ""},
		{"transports/" + id.String() + "/2026-AB-04/timeline", false, ""},
		{"tiers/dmsg/2026-05-04/timeline", false, ""},
		{"", false, ""},
	}
	for _, c := range cases {
		gotID, gotDate, ok := parseTransportTimelinePath(c.path)
		if ok != c.want {
			t.Errorf("parseTransportTimelinePath(%q) ok = %v, want %v", c.path, ok, c.want)
			continue
		}
		if c.want {
			if gotID != id {
				t.Errorf("parseTransportTimelinePath(%q) id = %v, want %v", c.path, gotID, id)
			}
			if gotDate != c.wantDate {
				t.Errorf("parseTransportTimelinePath(%q) date = %q, want %q", c.path, gotDate, c.wantDate)
			}
		}
	}
}

// Dispatcher routes timeline-shaped leaves into IngestTransportTimeline
// without disturbing the bandwidth/latency/heartbeat path. Verifies
// payload pass-through (the leaf bytes are the bitmap; no JSON parse).
func TestDispatchLeafTimeline(t *testing.T) {
	id := uuid.New()
	pk := cipher.PubKey{}
	bitmap := make([]byte, 36)
	for i := range bitmap {
		bitmap[i] = byte(i)
	}

	sink := &recordingSink{}
	a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
	a.dispatchLeaf("transports/"+id.String()+"/2026-05-04/timeline", bitmap, pk)

	if len(sink.timelines) != 1 {
		t.Fatalf("expected 1 timeline call, got %d", len(sink.timelines))
	}
	if sink.timelines[0].tpID != id {
		t.Errorf("tpID = %v, want %v", sink.timelines[0].tpID, id)
	}
	if sink.timelines[0].date != "2026-05-04" {
		t.Errorf("date = %q, want %q", sink.timelines[0].date, "2026-05-04")
	}
	if !bytesEqual(sink.timelines[0].bitmap, bitmap) {
		t.Errorf("bitmap mismatch: got %x want %x", sink.timelines[0].bitmap, bitmap)
	}
	// No bandwidth/latency/heartbeat side-effects.
	if sink.bandwidths != 0 || len(sink.latencies) != 0 || len(sink.heartbeats) != 0 {
		t.Errorf("unexpected side effects: bw=%d lat=%d hb=%d", sink.bandwidths, len(sink.latencies), len(sink.heartbeats))
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// recordingSink captures sink calls so tests can assert what dispatchLeaf
// chose to forward (vs drop on the partial-zero gate).
type recordingSink struct {
	mu         sync.Mutex
	bandwidths int
	latencies  []struct{ min, max, avg float64 }
	heartbeats []struct {
		tpType string
		at     time.Time
	}
	timelines []struct {
		tpID   uuid.UUID
		date   string
		bitmap []byte
	}
}

func (s *recordingSink) UpdateBandwidth(_ context.Context, _ string, _ cipher.PubKey, _, _ uint64) error {
	s.mu.Lock()
	s.bandwidths++
	s.mu.Unlock()
	return nil
}
func (s *recordingSink) UpdateLatency(_ context.Context, _ string, minMS, maxMS, avgMS float64) error {
	s.mu.Lock()
	s.latencies = append(s.latencies, struct{ min, max, avg float64 }{minMS, maxMS, avgMS})
	s.mu.Unlock()
	return nil
}
func (s *recordingSink) RecordTransportHeartbeat(_ context.Context, _ uuid.UUID, tpType string, at time.Time) error {
	s.mu.Lock()
	s.heartbeats = append(s.heartbeats, struct {
		tpType string
		at     time.Time
	}{tpType, at})
	s.mu.Unlock()
	return nil
}
func (s *recordingSink) IngestTransportTimeline(_ context.Context, tpID uuid.UUID, date string, bitmap []byte) error {
	s.mu.Lock()
	cp := append([]byte(nil), bitmap...)
	s.timelines = append(s.timelines, struct {
		tpID   uuid.UUID
		date   string
		bitmap []byte
	}{tpID, date, cp})
	s.mu.Unlock()
	return nil
}

func TestDispatchLeafLatencyGate(t *testing.T) {
	id := uuid.New()
	pk := cipher.PubKey{}
	now := time.Now().UTC()

	cases := []struct {
		name      string
		snap      liveSnapshot
		wantBwHit bool
		wantLat   bool
	}{
		{
			name:      "all zero — bandwidth still fires (so tx counters update), latency dropped",
			snap:      liveSnapshot{SampledAt: now},
			wantBwHit: true,
			wantLat:   false,
		},
		{
			name:      "min=0, partial measurement — must NOT clobber stored latency",
			snap:      liveSnapshot{LatencyMinMS: 0, LatencyMaxMS: 30, LatencyAvgMS: 18, SampledAt: now},
			wantBwHit: true,
			wantLat:   false,
		},
		{
			name:      "max=0, partial measurement — drop",
			snap:      liveSnapshot{LatencyMinMS: 10, LatencyMaxMS: 0, LatencyAvgMS: 18, SampledAt: now},
			wantBwHit: true,
			wantLat:   false,
		},
		{
			name:      "all positive — write through",
			snap:      liveSnapshot{LatencyMinMS: 10, LatencyMaxMS: 30, LatencyAvgMS: 18, SampledAt: now},
			wantBwHit: true,
			wantLat:   true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &recordingSink{}
			a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
			leaf, err := json.Marshal(c.snap)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			a.dispatchLeaf("transports/"+id.String()+"/current", leaf, pk)
			if c.wantBwHit && sink.bandwidths != 1 {
				t.Errorf("expected 1 bandwidth call, got %d", sink.bandwidths)
			}
			if c.wantLat && len(sink.latencies) != 1 {
				t.Errorf("expected latency forwarded, got %d calls", len(sink.latencies))
			}
			if !c.wantLat && len(sink.latencies) != 0 {
				t.Errorf("expected latency dropped, got %+v", sink.latencies)
			}
		})
	}
}

// Heartbeats are gated on snap.Type — old visors that don't carry
// the field must not produce a sink call (the store would early-return
// on the type filter, but routing here saves the round-trip and keeps
// the dispatch contract observable).
func TestDispatchLeafHeartbeatGate(t *testing.T) {
	id := uuid.New()
	pk := cipher.PubKey{}
	now := time.Now().UTC()

	cases := []struct {
		name       string
		snap       liveSnapshot
		wantHB     bool
		wantHBType string
	}{
		{
			name:   "no type — pre-uptime visor, skip heartbeat",
			snap:   liveSnapshot{SentBytes: 100, SampledAt: now},
			wantHB: false,
		},
		{
			name:       "stcpr — record heartbeat",
			snap:       liveSnapshot{Type: "stcpr", SentBytes: 100, SampledAt: now},
			wantHB:     true,
			wantHBType: "stcpr",
		},
		{
			name:       "sudph — record heartbeat",
			snap:       liveSnapshot{Type: "sudph", SentBytes: 100, SampledAt: now},
			wantHB:     true,
			wantHBType: "sudph",
		},
		{
			name:       "type pass-through (filter happens in store, not dispatch)",
			snap:       liveSnapshot{Type: "dmsg", SampledAt: now},
			wantHB:     true,
			wantHBType: "dmsg",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &recordingSink{}
			a := &Aggregator{sink: sink, log: logging.MustGetLogger("test")}
			leaf, err := json.Marshal(c.snap)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			a.dispatchLeaf("transports/"+id.String()+"/current", leaf, pk)
			if c.wantHB {
				if len(sink.heartbeats) != 1 {
					t.Fatalf("expected 1 heartbeat call, got %d", len(sink.heartbeats))
				}
				if got := sink.heartbeats[0].tpType; got != c.wantHBType {
					t.Errorf("heartbeat type = %q, want %q", got, c.wantHBType)
				}
				// Slot accuracy: the at field must be the snap's
				// SampledAt, not a re-clocked time.Now() inside the
				// dispatcher. Otherwise leaf-arrival skew would
				// shift the slot bit.
				if !sink.heartbeats[0].at.Equal(c.snap.SampledAt) {
					t.Errorf("heartbeat at = %v, want SampledAt %v", sink.heartbeats[0].at, c.snap.SampledAt)
				}
			} else if len(sink.heartbeats) != 0 {
				t.Errorf("expected no heartbeat, got %+v", sink.heartbeats)
			}
		})
	}
}
