// Package skysocksc cmd/skywire-cli/commands/proxy/mux_telemetry_test.go c4-vis-cli
package skysocksc

import (
	"testing"
	"time"
)

func rgWith(legs ...muxLegInfo) muxRouteGroupInfo {
	rg := muxRouteGroupInfo{MuxEnabled: true}
	rg.Desc.SrcPK = "src"
	rg.Desc.SrcPort = 1
	rg.Desc.DstPK = "dst"
	rg.Desc.DstPort = 2
	rg.Legs = legs
	return rg
}

func recTypes(recs []muxTeleRecord) map[string]int {
	m := map[string]int{}
	for _, r := range recs {
		m[r.Type]++
	}
	return m
}

// TestTelemetryFirstPollEstablishes: the first poll seeds state and emits an
// "established" per leg plus one "sample", with zero inst rates (no prior).
func TestTelemetryFirstPollEstablishes(t *testing.T) {
	mt := newMuxTelemetry("skysocks-client")
	t0 := time.Unix(1000, 0)
	recs := mt.build([]muxRouteGroupInfo{rgWith(
		muxLegInfo{Index: 0, TpType: "dmsg", RemotePK: "pkA", Alive: true},
		muxLegInfo{Index: 1, TpType: "dmsg", RemotePK: "pkB", Alive: true},
	)}, t0)

	types := recTypes(recs)
	if types["established"] != 2 {
		t.Errorf("established = %d, want 2; recs=%+v", types["established"], recs)
	}
	if types["sample"] != 1 {
		t.Errorf("sample = %d, want 1", types["sample"])
	}
	for _, r := range recs {
		if r.Type != "sample" {
			continue
		}
		for _, l := range r.Legs {
			if l.InstSendBps != 0 || l.InstRecvBps != 0 {
				t.Errorf("first poll leg %d should have 0 rates, got send=%v recv=%v", l.RouteIndex, l.InstSendBps, l.InstRecvBps)
			}
		}
	}
}

// TestTelemetryInstBpsFromDelta: the second poll computes inst bps from the
// byte delta over the elapsed interval.
func TestTelemetryInstBpsFromDelta(t *testing.T) {
	mt := newMuxTelemetry("skysocks-client")
	t0 := time.Unix(1000, 0)
	mt.build([]muxRouteGroupInfo{rgWith(
		muxLegInfo{Index: 0, TpType: "dmsg", RemotePK: "pkA", Alive: true, SentBytes: 1000, RecvBytes: 2000},
	)}, t0)

	// +2s, +4000 sent, +8000 recv → 2000 B/s send, 4000 B/s recv.
	recs := mt.build([]muxRouteGroupInfo{rgWith(
		muxLegInfo{Index: 0, TpType: "dmsg", RemotePK: "pkA", Alive: true, SentBytes: 5000, RecvBytes: 10000},
	)}, t0.Add(2*time.Second))

	var sample *muxTeleRecord
	for i := range recs {
		if recs[i].Type == "sample" {
			sample = &recs[i]
		}
	}
	if sample == nil || len(sample.Legs) != 1 {
		t.Fatalf("expected one sample with one leg, got %+v", recs)
	}
	if got := sample.Legs[0].InstSendBps; got != 2000 {
		t.Errorf("InstSendBps = %v, want 2000", got)
	}
	if got := sample.Legs[0].InstRecvBps; got != 4000 {
		t.Errorf("InstRecvBps = %v, want 4000", got)
	}
}

// TestTelemetryHotSwapEvents: a warm-standby hot-swap (leg 2 active→standby,
// leg 3 standby→active between polls) emits a demoted+promoted pair and marks
// the legs' gate_state accordingly — the gate-5 no-dip swap made observable.
func TestTelemetryHotSwapEvents(t *testing.T) {
	mt := newMuxTelemetry("skysocks-client")
	t0 := time.Unix(1000, 0)
	// Poll 1: leg 2 active, leg 3 warm standby.
	mt.build([]muxRouteGroupInfo{rgWith(
		muxLegInfo{Index: 2, TpType: "dmsg", RemotePK: "pkC", Alive: true, Standby: false},
		muxLegInfo{Index: 3, TpType: "dmsg", RemotePK: "pkD", Alive: true, Standby: true},
	)}, t0)
	// Poll 2: swapped — leg 2 demoted to standby, leg 3 promoted to active.
	recs := mt.build([]muxRouteGroupInfo{rgWith(
		muxLegInfo{Index: 2, TpType: "dmsg", RemotePK: "pkC", Alive: true, Standby: true},
		muxLegInfo{Index: 3, TpType: "dmsg", RemotePK: "pkD", Alive: true, Standby: false},
	)}, t0.Add(time.Second))

	types := recTypes(recs)
	if types["promoted"] != 1 {
		t.Errorf("promoted = %d, want 1; recs=%+v", types["promoted"], recs)
	}
	if types["demoted"] != 1 {
		t.Errorf("demoted = %d, want 1; recs=%+v", types["demoted"], recs)
	}
	// gate_state in the sample must reflect the swap.
	for _, r := range recs {
		if r.Type != "sample" {
			continue
		}
		for _, l := range r.Legs {
			switch l.RouteIndex {
			case 2:
				if l.GateState != "standby" {
					t.Errorf("leg 2 gate_state = %q, want standby", l.GateState)
				}
			case 3:
				if l.GateState != "active" {
					t.Errorf("leg 3 gate_state = %q, want active", l.GateState)
				}
			}
		}
	}
}

// TestTelemetryDropAndFail: a leg that disappears emits "dropped"; a leg that
// goes not-alive but stays present emits "failed".
func TestTelemetryDropAndFail(t *testing.T) {
	mt := newMuxTelemetry("skysocks-client")
	t0 := time.Unix(1000, 0)
	mt.build([]muxRouteGroupInfo{rgWith(
		muxLegInfo{Index: 0, TpType: "dmsg", RemotePK: "pkA", Alive: true},
		muxLegInfo{Index: 1, TpType: "dmsg", RemotePK: "pkB", Alive: true},
	)}, t0)
	// Poll 2: leg 1 gone entirely; leg 0 still present but dead.
	recs := mt.build([]muxRouteGroupInfo{rgWith(
		muxLegInfo{Index: 0, TpType: "dmsg", RemotePK: "pkA", Alive: false},
	)}, t0.Add(time.Second))

	types := recTypes(recs)
	if types["dropped"] != 1 {
		t.Errorf("dropped = %d, want 1; recs=%+v", types["dropped"], recs)
	}
	if types["failed"] != 1 {
		t.Errorf("failed = %d, want 1; recs=%+v", types["failed"], recs)
	}
}

func TestMuxTeleIndexFromKey(t *testing.T) {
	cases := map[string]int{"src:1>dst:2#0": 0, "src:1>dst:2#7": 7, "a#b#12": 12, "no-hash": 0}
	for k, want := range cases {
		if got := muxTeleIndexFromKey(k); got != want {
			t.Errorf("muxTeleIndexFromKey(%q) = %d, want %d", k, got, want)
		}
	}
}
