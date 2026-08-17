package rpcgrpc

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// mustPK parses a hex public key or fails the test.
func mustPK(t *testing.T, hex string) cipher.PubKey {
	t.Helper()
	var pk cipher.PubKey
	if err := pk.Set(hex); err != nil {
		t.Fatalf("bad pk %q: %v", hex, err)
	}
	return pk
}

// TestMuxBwIntermediates pins the intermediate-extraction used by the
// disjoint-route planner: every hop endpoint that is neither src nor dst,
// de-duplicated, in first-seen order. A direct (single-hop) route has none.
// This is the key the planner uses to reject routes that share an
// intermediate — the crux of making the N mux routes actually disjoint.
func TestMuxBwIntermediates(t *testing.T) {
	src := mustPK(t, "03cb5eb5741df4d66492f96f89b55861def1e23850583c499805d96e053f9bceac")
	dst := mustPK(t, "0248bac07d82b54864959d293181ee6c3dc6e1771e28cca8dd80acac94e36aa164")
	mid1 := mustPK(t, "02880cc291ef1620b7d69b3b7b5cadb30d2d97bfe9070e07f6f0c4b6f7a5c8a17a")
	mid2 := mustPK(t, "026d060827a030ef1ea39ee67555c9800e8ca539e6e80d735b98a73c311d8d9f51")
	tp := uuid.New()

	t.Run("two-hop route yields the single intermediate", func(t *testing.T) {
		r := routing.Route{Hops: []routing.Hop{
			{From: src, To: mid1, TpID: tp},
			{From: mid1, To: dst, TpID: tp},
		}}
		got := muxBwIntermediates(r, src, dst)
		if len(got) != 1 || got[0] != mid1 {
			t.Fatalf("got %v, want [%s]", got, mid1)
		}
	})

	t.Run("three-hop route yields both intermediates, deduped", func(t *testing.T) {
		r := routing.Route{Hops: []routing.Hop{
			{From: src, To: mid1, TpID: tp},
			{From: mid1, To: mid2, TpID: tp},
			{From: mid2, To: dst, TpID: tp},
		}}
		got := muxBwIntermediates(r, src, dst)
		if len(got) != 2 || got[0] != mid1 || got[1] != mid2 {
			t.Fatalf("got %v, want [%s %s]", got, mid1, mid2)
		}
	})

	t.Run("direct route has no intermediates", func(t *testing.T) {
		r := routing.Route{Hops: []routing.Hop{{From: src, To: dst, TpID: tp}}}
		if got := muxBwIntermediates(r, src, dst); len(got) != 0 {
			t.Fatalf("direct route intermediates = %v, want empty", got)
		}
	})
}

// TestMuxBwRoutingHopsToInfoAndReverse pins the calc-route → explicit-dial
// conversion: forward hops carry TpID/From/To (TpType intentionally dropped,
// as PingContextWithRoute resolves by TpID), and the reverse is the forward
// reversed with From/To swapped — matching the shape `route calc` emits and
// `proxy mux set --legs` dials.
func TestMuxBwRoutingHopsToInfoAndReverse(t *testing.T) {
	src := mustPK(t, "03cb5eb5741df4d66492f96f89b55861def1e23850583c499805d96e053f9bceac")
	dst := mustPK(t, "0248bac07d82b54864959d293181ee6c3dc6e1771e28cca8dd80acac94e36aa164")
	mid := mustPK(t, "02880cc291ef1620b7d69b3b7b5cadb30d2d97bfe9070e07f6f0c4b6f7a5c8a17a")
	tp1 := uuid.New()
	tp2 := uuid.New()

	fwd := muxBwRoutingHopsToInfo([]routing.Hop{
		{From: src, To: mid, TpID: tp1},
		{From: mid, To: dst, TpID: tp2},
	})
	if len(fwd) != 2 {
		t.Fatalf("forward len = %d, want 2", len(fwd))
	}
	if fwd[0].TpID != tp1.String() || fwd[0].From != src.String() || fwd[0].To != mid.String() {
		t.Errorf("forward[0] = %+v", fwd[0])
	}
	if fwd[0].TpType != "" {
		t.Errorf("TpType should be empty (resolved by TpID), got %q", fwd[0].TpType)
	}

	rev := muxBwReverseHops(fwd)
	if len(rev) != 2 {
		t.Fatalf("reverse len = %d, want 2", len(rev))
	}
	// reverse[0] is forward[last] with From/To swapped.
	if rev[0].TpID != tp2.String() || rev[0].From != dst.String() || rev[0].To != mid.String() {
		t.Errorf("reverse[0] = %+v, want swapped last forward hop", rev[0])
	}
	if rev[1].TpID != tp1.String() || rev[1].From != mid.String() || rev[1].To != src.String() {
		t.Errorf("reverse[1] = %+v, want swapped first forward hop", rev[1])
	}
}

// TestNormalizeMuxBwRequestIdleBaselineImpliesProbeRtt pins the
// implies-relationship: setting idle_baseline_duration_ns > 0 turns
// ProbeRTT on at the handler regardless of what the caller passed.
// Without this, an operator who sets --idle-baseline but forgets
// --probe-rtt would get zero idle probes — no queueing-delay
// signal, and the failure mode would be silent (no error, just
// empty distributions in Done).
func TestNormalizeMuxBwRequestIdleBaselineImpliesProbeRtt(t *testing.T) {
	pk := "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c"

	t.Run("idle_baseline > 0 forces ProbeRTT on", func(t *testing.T) {
		c, err := normalizeMuxBwRequest(&MuxBandwidthRequest{
			TargetPk:               pk,
			IdleBaselineDurationNs: (5 * time.Second).Nanoseconds(),
			ProbeRtt:               false, // operator forgot to set it
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !c.ProbeRTT {
			t.Errorf("IdleBaselineDuration > 0 should force ProbeRTT = true")
		}
		if c.IdleBaselineDuration != 5*time.Second {
			t.Errorf("IdleBaselineDuration = %v, want 5s", c.IdleBaselineDuration)
		}
	})

	t.Run("idle_baseline = 0 leaves ProbeRTT alone", func(t *testing.T) {
		c, err := normalizeMuxBwRequest(&MuxBandwidthRequest{
			TargetPk:               pk,
			IdleBaselineDurationNs: 0,
			ProbeRtt:               false,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.ProbeRTT {
			t.Errorf("ProbeRTT should stay false when IdleBaseline=0 and caller didn't set it")
		}
	})
}

// TestNormalizeMuxBwRequest pins the default-fallback table. Without
// these, a zero-valued request would dial zero routes (no work),
// pump for zero duration (no measurement), and emit zero samples
// (no signal). Concrete defaults keep an unconfigured call usable.
func TestNormalizeMuxBwRequest(t *testing.T) {
	t.Run("invalid PK rejected", func(t *testing.T) {
		_, err := normalizeMuxBwRequest(&MuxBandwidthRequest{TargetPk: "not-a-pk"})
		if err == nil {
			t.Errorf("expected error for malformed PK, got nil")
		}
	})

	validPK := "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c"

	t.Run("zero values fall back to documented defaults", func(t *testing.T) {
		c, err := normalizeMuxBwRequest(&MuxBandwidthRequest{TargetPk: validPK})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Routes != defaultMuxBwRoutes {
			t.Errorf("Routes = %d, want %d", c.Routes, defaultMuxBwRoutes)
		}
		if c.Duration != defaultMuxBwDuration {
			t.Errorf("Duration = %v, want %v", c.Duration, defaultMuxBwDuration)
		}
		if c.PacketSizeKb != defaultMuxBwPacketSizeKb {
			t.Errorf("PacketSizeKb = %d, want %d", c.PacketSizeKb, defaultMuxBwPacketSizeKb)
		}
		if c.SetupTimeout != defaultMuxBwSetupTimeout {
			t.Errorf("SetupTimeout = %v, want %v", c.SetupTimeout, defaultMuxBwSetupTimeout)
		}
		if c.ProbeInterval != defaultMuxBwProbeInterval {
			t.Errorf("ProbeInterval = %v, want %v", c.ProbeInterval, defaultMuxBwProbeInterval)
		}
		if c.SampleInterval != defaultMuxBwSampleInterval {
			t.Errorf("SampleInterval = %v, want %v", c.SampleInterval, defaultMuxBwSampleInterval)
		}
	})

	t.Run("explicit values pass through", func(t *testing.T) {
		req := &MuxBandwidthRequest{
			TargetPk:         validPK,
			Routes:           4,
			DurationNs:       (60 * time.Second).Nanoseconds(),
			PacketSizeKb:     64,
			MinHops:          2,
			SetupTimeoutNs:   (15 * time.Second).Nanoseconds(),
			ProbeRtt:         true,
			ProbeIntervalNs:  (50 * time.Millisecond).Nanoseconds(),
			SampleIntervalNs: (500 * time.Millisecond).Nanoseconds(),
			LocalRoute:       true,
		}
		c, err := normalizeMuxBwRequest(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Routes != 4 || c.PacketSizeKb != 64 || c.MinHops != 2 {
			t.Errorf("int fields lost: %+v", c)
		}
		if c.Duration != 60*time.Second || c.SetupTimeout != 15*time.Second {
			t.Errorf("duration fields lost: %+v", c)
		}
		if c.ProbeInterval != 50*time.Millisecond || c.SampleInterval != 500*time.Millisecond {
			t.Errorf("interval fields lost: %+v", c)
		}
		if !c.ProbeRTT || !c.LocalRoute {
			t.Errorf("bool fields lost: %+v", c)
		}
	})

	t.Run("negative values fall back to defaults", func(t *testing.T) {
		// Proto int32 lets negatives through the wire; the handler
		// should treat them as "default" rather than propagate
		// nonsense into the pump loop (a negative Routes count
		// would never reach the dial loop, but a negative Duration
		// would make context.WithTimeout return immediately).
		c, err := normalizeMuxBwRequest(&MuxBandwidthRequest{
			TargetPk:     validPK,
			Routes:       -5,
			PacketSizeKb: -100,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Routes != defaultMuxBwRoutes {
			t.Errorf("negative Routes should default to %d, got %d", defaultMuxBwRoutes, c.Routes)
		}
		if c.PacketSizeKb != defaultMuxBwPacketSizeKb {
			t.Errorf("negative PacketSizeKb should default to %d, got %d", defaultMuxBwPacketSizeKb, c.PacketSizeKb)
		}
	})
}

// TestInt64ToFloat pins the conversion helper that bridges
// muxBwProbeLoop's int64 sample accumulator into aggregatePingSamples
// (which operates on float64). Without this conversion the Done
// event's probe stats would be wrong.
func TestInt64ToFloat(t *testing.T) {
	in := []int64{1_000_000, 2_500_000, 100_000_000}
	out := int64ToFloat(in)
	if len(out) != len(in) {
		t.Fatalf("length mismatch: got %d want %d", len(out), len(in))
	}
	for i, v := range in {
		if out[i] != float64(v) {
			t.Errorf("out[%d] = %v, want %v", i, out[i], float64(v))
		}
	}
	// Empty input → empty output (not nil — the slice should be
	// safe to range over without a nil-check).
	if got := int64ToFloat(nil); got == nil || len(got) != 0 {
		t.Errorf("nil input should yield empty slice, got %v", got)
	}
}

// TestMuxBwRouteStateAtomics pins the atomic counter semantics
// used by the pump goroutines + sampler. Each pump writes to its
// own routeState via Add(); the sampler reads via Load(). Race-free
// by virtue of sync/atomic — pinned here so accidental conversion
// to a plain field doesn't silently re-introduce a data race.
func TestMuxBwRouteStateAtomics(t *testing.T) {
	rs := &muxBwRouteState{index: 7}
	rs.bytesSent.Add(1024)
	rs.bytesRecv.Add(2048)
	rs.established.Store(true)
	rs.activeFlag.Store(true)

	if got := rs.bytesSent.Load(); got != 1024 {
		t.Errorf("bytesSent = %d, want 1024", got)
	}
	if got := rs.bytesRecv.Load(); got != 2048 {
		t.Errorf("bytesRecv = %d, want 2048", got)
	}
	if !rs.established.Load() {
		t.Errorf("established should be true")
	}
	if !rs.activeFlag.Load() {
		t.Errorf("activeFlag should be true")
	}

	// Flip activeFlag — the pump-end behavior.
	rs.activeFlag.Store(false)
	if rs.activeFlag.Load() {
		t.Errorf("activeFlag should be false after Store(false)")
	}
}

// TestBuildRouteFailureEvent pins the field-population semantics of
// the MuxRouteFailure builder. The builder is the only logic between
// detecting PingOnceWithEcho's error and emitting the route_failure
// event on the wire — testing it directly avoids standing up a full
// VisorAPI mock just to verify field shapes.
//
// Regression target: pre-this-event, pump errors were silently
// logged to s.log.Debugf and the pump goroutine returned, leaving
// the operator unable to distinguish "established but never
// delivered bytes" from "delivered some bytes then died" — both
// surfaced as a drop in active_routes with no event.
func TestBuildRouteFailureEvent(t *testing.T) {
	t.Run("populates index + error + cumulative bytes", func(t *testing.T) {
		rs := &muxBwRouteState{index: 3}
		rs.bytesSent.Store(12345)
		rs.bytesRecv.Store(11000)

		ev := buildRouteFailureEvent(rs, errors.New("EOF"), time.Now().Add(-2*time.Second))
		if ev.RouteIndex != 3 {
			t.Errorf("RouteIndex = %d, want 3", ev.RouteIndex)
		}
		if ev.ErrorMessage != "EOF" {
			t.Errorf("ErrorMessage = %q, want %q", ev.ErrorMessage, "EOF")
		}
		if ev.BytesSentBeforeFailure != 12345 {
			t.Errorf("BytesSentBeforeFailure = %d, want 12345", ev.BytesSentBeforeFailure)
		}
		if ev.BytesReceivedBeforeFailure != 11000 {
			t.Errorf("BytesReceivedBeforeFailure = %d, want 11000", ev.BytesReceivedBeforeFailure)
		}
		// Should reflect ~2s elapsed since pumpStart (loose bound for clock variance).
		if ev.ElapsedNs < int64(1500*time.Millisecond) || ev.ElapsedNs > int64(3*time.Second) {
			t.Errorf("ElapsedNs = %d (~%.2fs), want ≈ 2s", ev.ElapsedNs, float64(ev.ElapsedNs)/1e9)
		}
	})

	t.Run("zero bytes before first-call failure is the common case", func(t *testing.T) {
		// First PingOnceWithEcho returns err before any bytes shipped.
		// This is the dominant repro on the 2026-05-21 mux-via-
		// intermediates measurements: route established, pump enters
		// loop, first call fails with "no route" / "i/o timeout" /
		// "route_group closed". Consumers need to distinguish
		// "established but never delivered" from "delivered some
		// bytes then died" — both happen but have different
		// implications for the operator's tuning decisions.
		rs := &muxBwRouteState{index: 0}
		ev := buildRouteFailureEvent(rs, errors.New("route_group closed"), time.Now())
		if ev.BytesSentBeforeFailure != 0 || ev.BytesReceivedBeforeFailure != 0 {
			t.Errorf("expected zero bytes for first-call failure; got sent=%d recv=%d",
				ev.BytesSentBeforeFailure, ev.BytesReceivedBeforeFailure)
		}
		if ev.ErrorMessage == "" {
			t.Error("ErrorMessage must be populated even when bytes are zero")
		}
	})
}
