package rpcgrpc

import (
	"testing"
	"time"
)

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
