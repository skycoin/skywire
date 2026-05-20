package setupmetrics

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
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

// dialErrStub is a test double that mimics router.DialError's
// DialFailedPK() interface without importing router (which would be a
// cycle). The collector matches DialError via an anonymous interface,
// so anything implementing that interface is treated the same way.
type dialErrStub struct {
	pk  cipher.PubKey
	msg string
}

func (d *dialErrStub) Error() string               { return d.msg }
func (d *dialErrStub) DialFailedPK() cipher.PubKey { return d.pk }
func (d *dialErrStub) Unwrap() error               { return nil }

// TestCollector_CircuitBreaker_SourceUnreachable verifies that when
// the id_reservation failure was caused by the SOURCE visor being
// unreachable (not the destination), the destination's circuit
// breaker is NOT tripped and the failure is reclassified as
// source_unreachable. Regression test for the production pathology
// where popular public visors were blocked for hours because flaky
// source visors kept tripping their circuit breaker.
func TestCollector_CircuitBreaker_SourceUnreachable(t *testing.T) {
	c := NewCollector(CollectorConfig{})
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()

	// Fail circuitFailureThreshold+2 times, each time with the SOURCE
	// as the failed dial target.
	for i := 0; i < circuitFailureThreshold+2; i++ {
		e := fmt.Errorf("failed to instantiate route id reserver: a dial attempt failed with: %w",
			&dialErrStub{pk: srcPK, msg: "dial " + srcPK.String() + "@136: dmsg error 202"})
		c.RecordRouteContext(context.Background(), srcPK, dstPK, 1)(&e)
	}

	if ok, reason := c.AllowDestination(dstPK); !ok {
		t.Fatalf("source-side failures should NOT trip dst breaker, got denied: %q", reason)
	}

	snap := c.Snapshot()
	if got := snap.FailuresByReason[ReasonSourceUnreachable]; got != circuitFailureThreshold+2 {
		t.Errorf("source_unreachable count=%d, want %d", got, circuitFailureThreshold+2)
	}
	if got := snap.FailuresByReason[ReasonIDReservation]; got != 0 {
		t.Errorf("id_reservation count=%d, want 0 (all should reclass to source_unreachable)", got)
	}
}

// TestCollector_CircuitBreaker_DestinationUnreachable verifies the
// opposite: when the failed dial PK matches the destination, the
// breaker DOES trip.
func TestCollector_CircuitBreaker_DestinationUnreachable(t *testing.T) {
	c := NewCollector(CollectorConfig{})
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()

	for i := 0; i < circuitFailureThreshold; i++ {
		e := fmt.Errorf("failed to instantiate route id reserver: a dial attempt failed with: %w",
			&dialErrStub{pk: dstPK, msg: "dial " + dstPK.String() + "@136: dmsg error 202"})
		c.RecordRouteContext(context.Background(), srcPK, dstPK, 1)(&e)
	}

	if ok, _ := c.AllowDestination(dstPK); ok {
		t.Fatal("destination-side failures should trip the dst breaker")
	}

	snap := c.Snapshot()
	if got := snap.FailuresByReason[ReasonIDReservation]; got != circuitFailureThreshold {
		t.Errorf("id_reservation count=%d, want %d", got, circuitFailureThreshold)
	}
}

// TestCollector_CircuitBreaker_IntermediateUnreachable verifies that
// when the failed dial PK was an intermediate hop (neither src nor
// dst), the destination's breaker stays closed and the
// intermediate's own breaker accumulates instead. Regression test
// for the disjoint-mux pathology: N parallel routes through N
// different intermediates would accumulate dst breaker hits per bad
// intermediate, locking out all attempts even via healthy
// intermediates.
func TestCollector_CircuitBreaker_IntermediateUnreachable(t *testing.T) {
	c := NewCollector(CollectorConfig{})
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	interPK, _ := cipher.GenerateKeyPair()

	// Fail circuitFailureThreshold+2 times, each time with the
	// INTERMEDIATE as the failed dial target.
	for i := 0; i < circuitFailureThreshold+2; i++ {
		e := fmt.Errorf("failed to instantiate route id reserver: a dial attempt failed with: %w",
			&dialErrStub{pk: interPK, msg: "dial " + interPK.String() + "@136: dmsg error 202"})
		c.RecordRouteContext(context.Background(), srcPK, dstPK, 2)(&e)
	}

	if ok, reason := c.AllowDestination(dstPK); !ok {
		t.Fatalf("intermediate-side failures should NOT trip dst breaker, got denied: %q", reason)
	}
	if ok, _ := c.AllowIntermediate(interPK); ok {
		t.Fatal("intermediate-side failures should trip the intermediate's breaker")
	}

	snap := c.Snapshot()
	if got := snap.FailuresByReason[ReasonIntermediateUnreachable]; got != circuitFailureThreshold+2 {
		t.Errorf("intermediate_unreachable count=%d, want %d", got, circuitFailureThreshold+2)
	}
	if got := snap.FailuresByReason[ReasonIDReservation]; got != 0 {
		t.Errorf("id_reservation count=%d, want 0 (all should reclass to intermediate_unreachable)", got)
	}
	// Destination's Failed counter should not have been incremented.
	for _, d := range snap.TopDestinations {
		if d.PK == dstPK.String() && d.Failed != 0 {
			t.Errorf("dst Failed=%d, want 0 (intermediate failures shouldn't blame the dst)", d.Failed)
		}
	}
}

// TestCollector_CircuitBreaker_IntermediateBreakerNotPoisoningDst
// asserts the asymmetry: an intermediate breaker tripping leaves the
// dst breaker closed, so routes through OTHER intermediates can
// still set up. Without this property the disjoint-mux fanout would
// be useless under any rate of intermediate flakiness.
func TestCollector_CircuitBreaker_IntermediateBreakerNotPoisoningDst(t *testing.T) {
	c := NewCollector(CollectorConfig{})
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	badInter, _ := cipher.GenerateKeyPair()
	goodInter, _ := cipher.GenerateKeyPair()

	// Trip the bad intermediate's breaker.
	for i := 0; i < circuitFailureThreshold; i++ {
		e := fmt.Errorf("failed to instantiate route id reserver: a dial attempt failed with: %w",
			&dialErrStub{pk: badInter, msg: "dial " + badInter.String() + "@136: timeout"})
		c.RecordRouteContext(context.Background(), srcPK, dstPK, 2)(&e)
	}

	// Bad intermediate denied; good intermediate + dst still allowed.
	if ok, _ := c.AllowIntermediate(badInter); ok {
		t.Fatal("bad intermediate breaker should be open")
	}
	if ok, _ := c.AllowIntermediate(goodInter); !ok {
		t.Fatal("good intermediate breaker should be closed")
	}
	if ok, _ := c.AllowDestination(dstPK); !ok {
		t.Fatal("dst breaker should be closed (intermediate failures must not poison it)")
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
