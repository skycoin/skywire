package setupmetrics

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

func TestCollector_RecordSuccess(t *testing.T) {
	c := NewCollector(CollectorConfig{})
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()

	var err error
	c.RecordRouteContext(context.Background(), src, dst, 3)(&err)

	snap := c.Snapshot()
	if snap.TotalRequests != 1 {
		t.Fatalf("total=%d, want 1", snap.TotalRequests)
	}
	if snap.Successful != 1 {
		t.Fatalf("successful=%d, want 1", snap.Successful)
	}
	if snap.Failed != 0 {
		t.Fatalf("failed=%d, want 0", snap.Failed)
	}
	if snap.RouteLengthHist[3] != 1 {
		t.Fatalf("route length hist[3]=%d, want 1", snap.RouteLengthHist[3])
	}
	if snap.SuccessRatePct != 100.0 {
		t.Fatalf("success rate=%.1f, want 100.0", snap.SuccessRatePct)
	}
	if snap.LastSuccessAt == nil {
		t.Fatal("LastSuccessAt is nil")
	}
}

func TestCollector_ClassifiesErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want FailureReason
	}{
		{"deadline", context.DeadlineExceeded, ReasonContextDeadline},
		{"canceled", context.Canceled, ReasonContextCanceled},
		{"id reservation", errors.New("reserve route ids: dial failed"), ReasonIDReservation},
		{"id no client", errors.New("no client available for 0x1234"), ReasonIDReservation},
		{"destination rules", errors.New("failed to broadcast rules to destination router: rpc error"), ReasonDestinationRules},
		{"intermediary rules", errors.New("failed to broadcast intermediary rules"), ReasonIntermediaryRules},
		{"rule generation", errors.New("no key for hop"), ReasonRuleGeneration},
		{"invalid route", errors.New("invalid route: null dst"), ReasonInvalidRoute},
		{"wrapped deadline", fmt.Errorf("route setup: %w", context.DeadlineExceeded), ReasonContextDeadline},
		{"unknown", errors.New("the sun exploded"), ReasonUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyError(context.Background(), tc.err)
			if got != tc.want {
				t.Fatalf("classifyError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestCollector_RecordFailure(t *testing.T) {
	c := NewCollector(CollectorConfig{})
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()

	err := errors.New("failed to reserve route ids: dial hop failed")
	c.RecordRouteContext(context.Background(), src, dst, 2)(&err)

	snap := c.Snapshot()
	if snap.Failed != 1 {
		t.Fatalf("failed=%d, want 1", snap.Failed)
	}
	if snap.Successful != 0 {
		t.Fatalf("successful=%d, want 0", snap.Successful)
	}
	if snap.FailuresByReason[ReasonIDReservation] != 1 {
		t.Fatalf("id_reservation count=%d, want 1", snap.FailuresByReason[ReasonIDReservation])
	}
	if len(snap.RecentFailures) != 1 {
		t.Fatalf("recent failures=%d, want 1", len(snap.RecentFailures))
	}
	rf := snap.RecentFailures[0]
	if rf.Reason != ReasonIDReservation {
		t.Errorf("recent[0].reason=%q, want %q", rf.Reason, ReasonIDReservation)
	}
	if rf.HopCount != 2 {
		t.Errorf("recent[0].hop_count=%d, want 2", rf.HopCount)
	}
	if rf.DstPK == "" {
		t.Error("recent[0].DstPK empty")
	}
	if snap.TopFailedDestinations[0].Failed != 1 {
		t.Errorf("top failed dest count=%d, want 1", snap.TopFailedDestinations[0].Failed)
	}
}

func TestCollector_ConcurrencyDrop(t *testing.T) {
	c := NewCollector(CollectorConfig{})
	c.RecordConcurrencyDrop()
	c.RecordConcurrencyDrop()

	snap := c.Snapshot()
	if snap.ConcurrencyDrops != 2 {
		t.Fatalf("concurrency drops=%d, want 2", snap.ConcurrencyDrops)
	}
	if snap.TotalRequests != 0 {
		t.Errorf("drops should not count toward total, got %d", snap.TotalRequests)
	}
}

func TestCollector_LatencyPercentiles(t *testing.T) {
	c := NewCollector(CollectorConfig{LatencyRingSize: 100})
	dst, _ := cipher.GenerateKeyPair()

	// Manually stuff the ring (shortcut around real timing) by calling
	// finish() with synthesized durations. Easiest: spin through
	// RecordRouteContext with short sleeps. Use finish directly via
	// exported API to keep the test deterministic.
	for i := 1; i <= 100; i++ {
		// Each attempt pretends to take (i * 1ms) by overriding start.
		start := time.Now().Add(-time.Duration(i) * time.Millisecond)
		var err error
		c.finish(context.Background(), cipher.PubKey{}, dst, 1, start, &err)
	}

	snap := c.Snapshot()
	l := snap.LatencyMs
	if l.Count != 100 {
		t.Fatalf("latency count=%d, want 100", l.Count)
	}
	if l.Min < 1 {
		t.Errorf("min=%d, want >=1", l.Min)
	}
	if l.P50 < l.Min || l.P50 > l.Max {
		t.Errorf("p50=%d out of [%d, %d]", l.P50, l.Min, l.Max)
	}
	if l.P95 <= l.P50 {
		t.Errorf("p95=%d should be >= p50=%d", l.P95, l.P50)
	}
}

func TestCollector_FailureRingEviction(t *testing.T) {
	c := NewCollector(CollectorConfig{FailureRingSize: 3})
	dst, _ := cipher.GenerateKeyPair()

	for i := 0; i < 5; i++ {
		err := fmt.Errorf("failure %d", i)
		c.RecordRouteContext(context.Background(), cipher.PubKey{}, dst, 1)(&err)
	}

	snap := c.Snapshot()
	if len(snap.RecentFailures) != 3 {
		t.Fatalf("recent failures=%d, want 3", len(snap.RecentFailures))
	}
	// Newest should be first.
	if snap.RecentFailures[0].Error != "failure 4" {
		t.Errorf("newest error=%q, want %q", snap.RecentFailures[0].Error, "failure 4")
	}
	if snap.RecentFailures[2].Error != "failure 2" {
		t.Errorf("oldest error=%q, want %q", snap.RecentFailures[2].Error, "failure 2")
	}
}

func TestCollector_CircuitBreaker(t *testing.T) {
	c := NewCollector(CollectorConfig{})
	dst, _ := cipher.GenerateKeyPair()

	// Breaker closed initially — all attempts allowed.
	if ok, _ := c.AllowDestination(dst); !ok {
		t.Fatal("initial state should allow the destination")
	}

	// id_reservation failures drive the breaker. One short of
	// threshold: still closed.
	idResErr := errors.New("failed to instantiate route id reserver: dmsg error 202 - cannot connect to delegated server")
	for i := 0; i < circuitFailureThreshold-1; i++ {
		e := idResErr
		c.RecordRouteContext(context.Background(), cipher.PubKey{}, dst, 1)(&e)
	}
	if ok, _ := c.AllowDestination(dst); !ok {
		t.Fatal("under threshold: should still allow")
	}

	// One more failure trips the breaker.
	e := idResErr
	c.RecordRouteContext(context.Background(), cipher.PubKey{}, dst, 1)(&e)
	if ok, reason := c.AllowDestination(dst); ok {
		t.Fatalf("threshold reached: should deny, got allowed (%q)", reason)
	}

	// Snapshot should reflect circuit=open on this destination.
	snap := c.Snapshot()
	var found bool
	for _, d := range snap.TopDestinations {
		if d.PK == dst.String() {
			found = true
			if d.Circuit != string(CircuitOpen) {
				t.Fatalf("snapshot circuit=%q, want open", d.Circuit)
			}
			break
		}
	}
	if !found {
		t.Fatal("destination not in top destinations")
	}

	// A success while breaker is open should close it.
	var okErr error
	c.RecordRouteContext(context.Background(), cipher.PubKey{}, dst, 1)(&okErr)
	if ok, _ := c.AllowDestination(dst); !ok {
		t.Fatal("after success: should allow again")
	}
}

// TestCollector_CircuitBreakerOnlyIDReservation verifies that other
// failure reasons do not trip the breaker — only dial-path failures
// should, because the others are local config / rule problems that
// waiting does not fix.
func TestCollector_CircuitBreakerOnlyIDReservation(t *testing.T) {
	c := NewCollector(CollectorConfig{})
	dst, _ := cipher.GenerateKeyPair()

	// Throw circuitFailureThreshold+1 non-id-reservation failures at
	// the destination.
	genErr := errors.New("generate rules: no key for hop")
	for i := 0; i < circuitFailureThreshold+1; i++ {
		e := genErr
		c.RecordRouteContext(context.Background(), cipher.PubKey{}, dst, 1)(&e)
	}

	if ok, _ := c.AllowDestination(dst); !ok {
		t.Fatal("non-id-reservation failures should not trip breaker")
	}
}

func TestCollector_Reset(t *testing.T) {
	c := NewCollector(CollectorConfig{})
	dst, _ := cipher.GenerateKeyPair()
	var ok error
	c.RecordRouteContext(context.Background(), cipher.PubKey{}, dst, 1)(&ok)
	bad := errors.New("boom")
	c.RecordRouteContext(context.Background(), cipher.PubKey{}, dst, 1)(&bad)

	c.Reset()

	snap := c.Snapshot()
	if snap.TotalRequests != 0 || snap.Successful != 0 || snap.Failed != 0 {
		t.Fatalf("counters not reset: %+v", snap)
	}
	if len(snap.RecentFailures) != 0 {
		t.Fatalf("recent failures not reset: %d", len(snap.RecentFailures))
	}
	if len(snap.RouteLengthHist) != 0 {
		t.Fatalf("route length hist not reset")
	}
}
