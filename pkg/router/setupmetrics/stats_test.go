package setupmetrics

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routersettings"
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
		c.finish(context.Background(), cipher.PubKey{}, dst, 1, start, &err, nil)
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
	enableCircuitBreaker(t)
	c := NewCollector(CollectorConfig{})
	dst, _ := cipher.GenerateKeyPair()

	// Breaker closed initially — all attempts allowed.
	if ok, _ := c.AllowDestination(dst, nil); !ok {
		t.Fatal("initial state should allow the destination")
	}

	// id_reservation failures drive the breaker. One short of
	// threshold: still closed.
	idResErr := errors.New("failed to instantiate route id reserver: dmsg error 202 - cannot connect to delegated server")
	for i := 0; i < circuitFailureThreshold()-1; i++ {
		e := idResErr
		c.RecordRouteContext(context.Background(), cipher.PubKey{}, dst, 1)(&e)
	}
	if ok, _ := c.AllowDestination(dst, nil); !ok {
		t.Fatal("under threshold: should still allow")
	}

	// One more failure trips the breaker.
	e := idResErr
	c.RecordRouteContext(context.Background(), cipher.PubKey{}, dst, 1)(&e)
	if ok, reason := c.AllowDestination(dst, nil); ok {
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
	if ok, _ := c.AllowDestination(dst, nil); !ok {
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
	enableCircuitBreaker(t)
	c := NewCollector(CollectorConfig{})
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()

	// Fail circuitFailureThreshold()+2 times, each time with the SOURCE
	// as the failed dial target.
	for i := 0; i < circuitFailureThreshold()+2; i++ {
		e := fmt.Errorf("failed to instantiate route id reserver: a dial attempt failed with: %w",
			&dialErrStub{pk: srcPK, msg: "dial " + srcPK.String() + "@136: dmsg error 202"})
		c.RecordRouteContext(context.Background(), srcPK, dstPK, 1)(&e)
	}

	if ok, reason := c.AllowDestination(dstPK, nil); !ok {
		t.Fatalf("source-side failures should NOT trip dst breaker, got denied: %q", reason)
	}

	snap := c.Snapshot()
	if got := snap.FailuresByReason[ReasonSourceUnreachable]; got != uint64(circuitFailureThreshold()+2) {
		t.Errorf("source_unreachable count=%d, want %d", got, circuitFailureThreshold()+2)
	}
	if got := snap.FailuresByReason[ReasonIDReservation]; got != 0 {
		t.Errorf("id_reservation count=%d, want 0 (all should reclass to source_unreachable)", got)
	}
}

// TestCollector_CircuitBreaker_DestinationUnreachable verifies the
// opposite: when the failed dial PK matches the destination, the
// breaker DOES trip.
func TestCollector_CircuitBreaker_DestinationUnreachable(t *testing.T) {
	enableCircuitBreaker(t)
	c := NewCollector(CollectorConfig{})
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()

	for i := 0; i < circuitFailureThreshold(); i++ {
		e := fmt.Errorf("failed to instantiate route id reserver: a dial attempt failed with: %w",
			&dialErrStub{pk: dstPK, msg: "dial " + dstPK.String() + "@136: dmsg error 202"})
		c.RecordRouteContext(context.Background(), srcPK, dstPK, 1)(&e)
	}

	if ok, _ := c.AllowDestination(dstPK, nil); ok {
		t.Fatal("destination-side failures should trip the dst breaker")
	}

	snap := c.Snapshot()
	if got := snap.FailuresByReason[ReasonIDReservation]; got != uint64(circuitFailureThreshold()) {
		t.Errorf("id_reservation count=%d, want %d", got, circuitFailureThreshold())
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
	enableCircuitBreaker(t)
	c := NewCollector(CollectorConfig{})
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	interPK, _ := cipher.GenerateKeyPair()

	// Fail circuitFailureThreshold()+2 times, each time with the
	// INTERMEDIATE as the failed dial target.
	for i := 0; i < circuitFailureThreshold()+2; i++ {
		e := fmt.Errorf("failed to instantiate route id reserver: a dial attempt failed with: %w",
			&dialErrStub{pk: interPK, msg: "dial " + interPK.String() + "@136: dmsg error 202"})
		c.RecordRouteContext(context.Background(), srcPK, dstPK, 2)(&e)
	}

	if ok, reason := c.AllowDestination(dstPK, nil); !ok {
		t.Fatalf("intermediate-side failures should NOT trip dst breaker, got denied: %q", reason)
	}
	if ok, _ := c.AllowIntermediate(interPK, nil); ok {
		t.Fatal("intermediate-side failures should trip the intermediate's breaker")
	}

	snap := c.Snapshot()
	if got := snap.FailuresByReason[ReasonIntermediateUnreachable]; got != uint64(circuitFailureThreshold()+2) {
		t.Errorf("intermediate_unreachable count=%d, want %d", got, circuitFailureThreshold()+2)
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
	enableCircuitBreaker(t)
	c := NewCollector(CollectorConfig{})
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	badInter, _ := cipher.GenerateKeyPair()
	goodInter, _ := cipher.GenerateKeyPair()

	// Trip the bad intermediate's breaker.
	for i := 0; i < circuitFailureThreshold(); i++ {
		e := fmt.Errorf("failed to instantiate route id reserver: a dial attempt failed with: %w",
			&dialErrStub{pk: badInter, msg: "dial " + badInter.String() + "@136: timeout"})
		c.RecordRouteContext(context.Background(), srcPK, dstPK, 2)(&e)
	}

	// Bad intermediate denied; good intermediate + dst still allowed.
	if ok, _ := c.AllowIntermediate(badInter, nil); ok {
		t.Fatal("bad intermediate breaker should be open")
	}
	if ok, _ := c.AllowIntermediate(goodInter, nil); !ok {
		t.Fatal("good intermediate breaker should be closed")
	}
	if ok, _ := c.AllowDestination(dstPK, nil); !ok {
		t.Fatal("dst breaker should be closed (intermediate failures must not poison it)")
	}
}

// TestCollector_CircuitBreakerOnlyIDReservation verifies that other
// failure reasons do not trip the breaker — only dial-path failures
// should, because the others are local config / rule problems that
// waiting does not fix.
func TestCollector_CircuitBreakerOnlyIDReservation(t *testing.T) {
	enableCircuitBreaker(t)
	c := NewCollector(CollectorConfig{})
	dst, _ := cipher.GenerateKeyPair()

	// Throw circuitFailureThreshold()+1 non-id-reservation failures at
	// the destination.
	genErr := errors.New("generate rules: no key for hop")
	for i := 0; i < circuitFailureThreshold()+1; i++ {
		e := genErr
		c.RecordRouteContext(context.Background(), cipher.PubKey{}, dst, 1)(&e)
	}

	if ok, _ := c.AllowDestination(dst, nil); !ok {
		t.Fatal("non-id-reservation failures should not trip breaker")
	}
}

func TestCollector_Reset(t *testing.T) {
	enableCircuitBreaker(t)
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

// ---------------------------------------------------------------------
// Half-open probe accounting.
//
// A breaker's half-open state admits exactly ONE probe request. The
// request that took the slot has to release it, whatever happens to
// that request — otherwise the breaker refuses every later caller with
// "probe in flight" until circuitMaxOpenDuration() force-resets it 30
// minutes later. That is the live 2026-09-16 failure these tests pin:
// an intermediate admitted as a probe via AllowIntermediate was never
// resolved by finish(), because finish() only ever touched the
// destination's breaker (on success) or the PK that failed to dial (on
// failure).
// ---------------------------------------------------------------------

// dialFailure builds the id_reservation error shape finish() parses,
// with pk as the hop that could not be dialed.
func dialFailure(pk cipher.PubKey) error {
	return fmt.Errorf("failed to instantiate route id reserver: a dial attempt failed with: %w",
		&dialErrStub{pk: pk, msg: "dial " + pk.String() + "@136: dmsg error 202"})
}

// tripBreakerVia drives pk's breaker to OPEN using dial failures
// attributed to pk.
func tripBreakerVia(t *testing.T, c *Collector, srcPK, dstPK, pk cipher.PubKey) {
	t.Helper()
	for i := 0; i < circuitFailureThreshold(); i++ {
		e := dialFailure(pk)
		c.RecordRouteContext(context.Background(), srcPK, dstPK, 2)(&e)
	}
}

// agePastOpenWindow backdates pk's breaker so the next caller is
// admitted as the half-open probe, without making the test sleep for
// circuitOpenDuration() (5 minutes).
func agePastOpenWindow(t *testing.T, c *Collector, pk cipher.PubKey) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	br, ok := c.breakers[pk.String()]
	if !ok {
		t.Fatalf("no breaker for %s", pk.String())
	}
	aged := time.Now().Add(-circuitOpenDuration() - time.Second)
	br.openedAt = aged
	br.firstOpenedAt = aged
}

func breakerOf(t *testing.T, c *Collector, pk cipher.PubKey) (BreakerState, bool) {
	t.Helper()
	bs, ok := c.Snapshot().Breakers[pk.String()]
	return bs, ok
}

// TestCollector_HalfOpenProbe_IntermediateClosedOnSuccess: an
// intermediate admitted as the half-open probe must have its breaker
// CLOSED when the request succeeds — the route that just came up ran
// through that hop, which is the proof the probe was testing for.
func TestCollector_HalfOpenProbe_IntermediateClosedOnSuccess(t *testing.T) {
	enableCircuitBreaker(t)
	c := NewCollector(CollectorConfig{})
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	interPK, _ := cipher.GenerateKeyPair()

	tripBreakerVia(t, c, srcPK, dstPK, interPK)
	if ok, _ := c.AllowIntermediate(interPK, nil); ok {
		t.Fatal("intermediate breaker should be open after threshold failures")
	}
	agePastOpenWindow(t, c, interPK)

	var probes ProbeHolder
	done := c.RecordRouteContextProbes(context.Background(), srcPK, dstPK, 2, &probes)
	if ok, reason := c.AllowIntermediate(interPK, &probes); !ok {
		t.Fatalf("probe should be admitted once the open window elapsed: %q", reason)
	}
	// A concurrent request must NOT also get in — one probe at a time.
	if ok, _ := c.AllowIntermediate(interPK, &ProbeHolder{}); ok {
		t.Fatal("a second concurrent probe should be refused")
	}

	var noErr error
	done(&noErr)

	if ok, reason := c.AllowIntermediate(interPK, &ProbeHolder{}); !ok {
		t.Fatalf("after a successful probe the breaker should be closed: %q", reason)
	}
	if bs, ok := breakerOf(t, c, interPK); ok {
		t.Fatalf("closed breaker should not appear in the snapshot: %+v", bs)
	}
}

// TestCollector_HalfOpenProbe_IntermediateReleasedOnDstFailure: the
// request that holds an intermediate's probe fails because the
// DESTINATION could not be dialed. That says nothing about the
// intermediate, so its probe slot must be released — breaker back to
// open, probe_in_flight false, next caller admitted as the new probe.
// Before the fix probeInFlight stayed true and every later route
// through this hop was refused for 30 minutes.
func TestCollector_HalfOpenProbe_IntermediateReleasedOnDstFailure(t *testing.T) {
	enableCircuitBreaker(t)
	c := NewCollector(CollectorConfig{})
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	interPK, _ := cipher.GenerateKeyPair()

	tripBreakerVia(t, c, srcPK, dstPK, interPK)
	agePastOpenWindow(t, c, interPK)

	var probes ProbeHolder
	done := c.RecordRouteContextProbes(context.Background(), srcPK, dstPK, 2, &probes)
	if ok, reason := c.AllowIntermediate(interPK, &probes); !ok {
		t.Fatalf("probe should be admitted: %q", reason)
	}
	// The request dies on the destination, not on this hop.
	e := dialFailure(dstPK)
	done(&e)

	bs, ok := breakerOf(t, c, interPK)
	if !ok {
		t.Fatal("intermediate breaker should still be tracked (it was never proven healthy)")
	}
	if bs.ProbeInFlight {
		t.Fatal("probe slot leaked: probe_in_flight still set after the holding request ended")
	}
	if bs.State != CircuitOpen {
		t.Fatalf("breaker state=%q, want open", bs.State)
	}
	if ok, reason := c.AllowIntermediate(interPK, &ProbeHolder{}); !ok {
		t.Fatalf("the next caller should become the new probe: %q", reason)
	}
	// ...and the destination's own breaker took the failure.
	if dbs, ok := breakerOf(t, c, dstPK); ok && dbs.ConsecutiveFails == 0 {
		t.Fatal("destination breaker should have recorded the dial failure")
	}
}

// TestCollector_HalfOpenProbe_DestinationReleasedOnNonDialFailure:
// same hole on the destination side. A destination probe whose request
// dies for a non-id_reservation reason (rule generation, an invalid
// route, another hop's breaker short-circuiting the setup) never
// reached the dial path, so the probe must be released rather than
// left in flight.
func TestCollector_HalfOpenProbe_DestinationReleasedOnNonDialFailure(t *testing.T) {
	enableCircuitBreaker(t)
	c := NewCollector(CollectorConfig{})
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()

	// Trip the destination's breaker (no DialError in the chain → the
	// legacy "blame the destination" branch).
	idResErr := errors.New("failed to instantiate route id reserver: dmsg error 202 - cannot connect to delegated server")
	for i := 0; i < circuitFailureThreshold(); i++ {
		e := idResErr
		c.RecordRouteContext(context.Background(), srcPK, dstPK, 1)(&e)
	}
	agePastOpenWindow(t, c, dstPK)

	var probes ProbeHolder
	done := c.RecordRouteContextProbes(context.Background(), srcPK, dstPK, 1, &probes)
	if ok, reason := c.AllowDestination(dstPK, &probes); !ok {
		t.Fatalf("destination probe should be admitted: %q", reason)
	}
	genErr := errors.New("generate rules: no key for hop")
	done(&genErr)

	bs, ok := breakerOf(t, c, dstPK)
	if !ok {
		t.Fatal("destination breaker should still be tracked")
	}
	if bs.ProbeInFlight {
		t.Fatal("probe slot leaked: probe_in_flight still set after the holding request ended")
	}
	if bs.State != CircuitOpen {
		t.Fatalf("breaker state=%q, want open", bs.State)
	}
	if bs.OpenedAt.IsZero() {
		t.Fatal("opened_at should be reported for a non-closed breaker")
	}
	if ok, reason := c.AllowDestination(dstPK, &ProbeHolder{}); !ok {
		t.Fatalf("the next caller should become the new probe: %q", reason)
	}
}

// TestCollector_SnapshotBreakers_OnlyNonClosed: the /stats view lists
// exactly the breakers that are refusing traffic, so an operator can
// see a stuck probe without correlating error strings.
func TestCollector_SnapshotBreakers_OnlyNonClosed(t *testing.T) {
	enableCircuitBreaker(t)
	c := NewCollector(CollectorConfig{})
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	interPK, _ := cipher.GenerateKeyPair()

	if got := c.Snapshot().Breakers; got != nil {
		t.Fatalf("no breakers tripped, want nil map, got %+v", got)
	}

	tripBreakerVia(t, c, srcPK, dstPK, interPK)

	snap := c.Snapshot()
	if len(snap.Breakers) != 1 {
		t.Fatalf("breakers=%+v, want exactly the intermediate", snap.Breakers)
	}
	bs, ok := snap.Breakers[interPK.String()]
	if !ok {
		t.Fatalf("intermediate %s missing from breakers: %+v", interPK.String(), snap.Breakers)
	}
	if bs.State != CircuitOpen || bs.ConsecutiveFails != circuitFailureThreshold() || bs.ProbeInFlight {
		t.Fatalf("breaker state=%+v, want open/%d fails/no probe", bs, circuitFailureThreshold())
	}
	if _, ok := snap.Breakers[dstPK.String()]; ok {
		t.Fatal("destination breaker is closed and must not be listed")
	}
}

// enableCircuitBreaker turns setup.circuit_breaker on for one test and puts
// the catalog back afterwards. The breaker ships OFF, so every test that
// asserts a lockout has to ask for one.
func enableCircuitBreaker(t *testing.T) {
	t.Helper()
	if err := routersettings.Set(routersettings.SetupCircuitBreaker.Name(), "true"); err != nil {
		t.Fatalf("enable setup.circuit_breaker: %v", err)
	}
	t.Cleanup(routersettings.Reset)
}

// The shipped default is OFF: a destination can fail any number of times in a
// row and still never be locked out. This is the 2026-09-18 incident — a visor
// holding 32 live route groups to one exit was refused every further setup to
// it because three failures the setup node blamed on that exit had tripped its
// breaker. Counting keeps working; only the lockout is gone.
func TestCollector_CircuitBreakerOffByDefault(t *testing.T) {
	t.Cleanup(routersettings.Reset)
	if routersettings.SetupCircuitBreaker.Bool() {
		t.Fatal("setup.circuit_breaker defaults to on; it must ship off")
	}

	c := NewCollector(CollectorConfig{})
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()

	const attempts = 10 // well past setup.circuit_fail_threshold
	for i := 0; i < attempts; i++ {
		var probes ProbeHolder
		ok, reason := c.AllowDestination(dst, &probes)
		if !ok {
			t.Fatalf("attempt %d refused with %q; the breaker is off", i, reason)
		}
		err := error(&dialErrStub{pk: dst, msg: "failed to instantiate route id reserver: dial failed"})
		c.RecordRouteContextProbes(context.Background(), src, dst, 2, &probes)(&err)
	}

	snap := c.Snapshot()
	if len(snap.Breakers) != 0 {
		t.Fatalf("breakers=%+v, want none open", snap.Breakers)
	}
	if got := snap.FailuresByReason[ReasonIDReservation]; got != attempts {
		t.Errorf("id_reservation count=%d, want %d — failures must still be counted", got, attempts)
	}
	if got := snap.FailuresByReason[ReasonCircuitOpen]; got != 0 {
		t.Errorf("circuit_open count=%d, want 0 — nothing may be short-circuited", got)
	}
	var probes ProbeHolder
	if ok, reason := c.AllowDestination(dst, &probes); !ok {
		t.Fatalf("destination refused after %d failures: %q", attempts, reason)
	}
}

// …and turning the knob back on restores exactly the old trip: the same burst
// opens the breaker at setup.circuit_fail_threshold and the next caller is
// refused.
func TestCollector_CircuitBreakerKnobRestoresTrip(t *testing.T) {
	enableCircuitBreaker(t)

	c := NewCollector(CollectorConfig{})
	src, _ := cipher.GenerateKeyPair()
	dst, _ := cipher.GenerateKeyPair()

	for i := 0; i < circuitFailureThreshold(); i++ {
		var probes ProbeHolder
		if ok, _ := c.AllowDestination(dst, &probes); !ok {
			t.Fatalf("attempt %d refused before the threshold was reached", i)
		}
		err := error(&dialErrStub{pk: dst, msg: "failed to instantiate route id reserver: dial failed"})
		c.RecordRouteContextProbes(context.Background(), src, dst, 2, &probes)(&err)
	}

	var probes ProbeHolder
	ok, reason := c.AllowDestination(dst, &probes)
	if ok {
		t.Fatal("destination still allowed with the breaker on past its threshold")
	}
	if reason == "" {
		t.Error("refusal carried no reason")
	}
	if bs, open := c.Snapshot().Breakers[dst.String()]; !open || bs.State != CircuitOpen {
		t.Fatalf("breaker=%+v open=%v, want open", bs, open)
	}
}
