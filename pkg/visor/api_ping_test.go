// Package visor — pkg/visor/api_ping_test.go: unit tests for the
// per-route teardown surface added when PingRouteRef became the
// pingState.conns map key.
package visor

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
)

// fakeConn satisfies net.Conn and records Close() calls for tests.
// Avoids dragging in netpipe / real sockets just to verify the
// teardown bookkeeping in the visor's pingState map.
type fakeConn struct {
	closed bool
	err    error
}

func (c *fakeConn) Read(_ []byte) (int, error)         { return 0, nil }
func (c *fakeConn) Write(_ []byte) (int, error)        { return 0, nil }
func (c *fakeConn) Close() error                       { c.closed = true; return c.err }
func (c *fakeConn) LocalAddr() net.Addr                { return nil }
func (c *fakeConn) RemoteAddr() net.Addr               { return nil }
func (c *fakeConn) SetDeadline(_ time.Time) error      { return nil } //nolint:unused
func (c *fakeConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(_ time.Time) error { return nil }

// helperPK constructs a deterministic 33-byte PubKey from an int.
// Avoids running the curve to generate real keys — we only need
// distinct values for map-key tests.
func helperPK(b byte) cipher.PubKey {
	var pk cipher.PubKey
	pk[0] = 2 // compressed-Y marker — any constant works for map equality
	pk[1] = b
	return pk
}

// newPingStateVisor builds the smallest Visor that the ping
// teardown methods touch. They only read v.ping.conns + v.ping.mu;
// nothing else. Keeps the test free of init plumbing.
func newPingStateVisor() *Visor {
	return &Visor{
		ping: pingState{
			conns: make(map[PingRouteRef]ping),
			mu:    new(sync.Mutex),
		},
	}
}

// TestPingRouteRefMapIsolation pins the central invariant: two
// PingRouteRef values that share PK but differ in Index are
// distinct map keys. If this regresses (e.g. someone redefines
// PingRouteRef to ignore Index), mux-bw's N parallel routes
// silently collapse back to a single shared conn.
func TestPingRouteRefMapIsolation(t *testing.T) {
	pk := helperPK(1)
	r0 := PingRouteRef{PK: pk, Index: 0}
	r1 := PingRouteRef{PK: pk, Index: 1}

	m := make(map[PingRouteRef]ping)
	m[r0] = ping{conn: &fakeConn{}}
	m[r1] = ping{conn: &fakeConn{}}

	if len(m) != 2 {
		t.Fatalf("two distinct refs should produce 2 map entries, got %d", len(m))
	}
	if m[r0].conn == m[r1].conn {
		t.Errorf("entries at different RouteIndex share a conn — map key is collapsing")
	}
}

// TestPingRoutePrimary pins the legacy-caller convenience: callers
// passing a bare PK keep working because PingRoutePrimary(pk) maps
// to Index 0.
func TestPingRoutePrimary(t *testing.T) {
	pk := helperPK(2)
	ref := PingRoutePrimary(pk)
	if ref.PK != pk {
		t.Errorf("PK mismatch: got %v want %v", ref.PK, pk)
	}
	if ref.Index != 0 {
		t.Errorf("Index should be 0 (primary), got %d", ref.Index)
	}
}

// TestStopPingClosesAllRoutesForPeer pins the legacy-semantics
// preservation: StopPing(pk) closes every route for that peer, not
// just the primary. This matters because existing callers
// (init_services teardown, hypervisor_handlers_reachability) call
// StopPing expecting "tear it all down for this PK".
func TestStopPingClosesAllRoutesForPeer(t *testing.T) {
	v := newPingStateVisor()
	pk := helperPK(3)

	c0, c1, c2 := &fakeConn{}, &fakeConn{}, &fakeConn{}
	v.ping.conns[PingRouteRef{PK: pk, Index: 0}] = ping{conn: c0}
	v.ping.conns[PingRouteRef{PK: pk, Index: 1}] = ping{conn: c1}
	v.ping.conns[PingRouteRef{PK: pk, Index: 2}] = ping{conn: c2}

	// Add a conn for an unrelated peer — it must NOT be touched.
	otherPK := helperPK(4)
	otherConn := &fakeConn{}
	v.ping.conns[PingRouteRef{PK: otherPK, Index: 0}] = ping{conn: otherConn}

	if err := v.StopPing(pk); err != nil {
		t.Fatalf("StopPing returned error: %v", err)
	}

	if !c0.closed || !c1.closed || !c2.closed {
		t.Errorf("expected all three routes for pk closed: got %v %v %v",
			c0.closed, c1.closed, c2.closed)
	}
	if otherConn.closed {
		t.Errorf("StopPing(pk) closed an unrelated peer's conn — bleed-over")
	}
	// All pk entries must be deleted; the other peer's entry stays.
	if _, ok := v.ping.conns[PingRouteRef{PK: pk, Index: 0}]; ok {
		t.Errorf("pk Index 0 still in map after StopPing")
	}
	if _, ok := v.ping.conns[PingRouteRef{PK: otherPK, Index: 0}]; !ok {
		t.Errorf("unrelated peer's entry was deleted")
	}
}

// TestStopPingRouteOnlyClosesOneRoute pins the new affordance: the
// caller can drop a single route without touching the parallel
// routes. mux-bw relies on this when one of N pump goroutines
// finishes / errors out.
func TestStopPingRouteOnlyClosesOneRoute(t *testing.T) {
	v := newPingStateVisor()
	pk := helperPK(5)

	c0, c1 := &fakeConn{}, &fakeConn{}
	v.ping.conns[PingRouteRef{PK: pk, Index: 0}] = ping{conn: c0}
	v.ping.conns[PingRouteRef{PK: pk, Index: 1}] = ping{conn: c1}

	if err := v.StopPingRoute(PingRouteRef{PK: pk, Index: 1}); err != nil {
		t.Fatalf("StopPingRoute returned error: %v", err)
	}

	if c0.closed {
		t.Errorf("Index 0 conn closed by StopPingRoute(Index 1)")
	}
	if !c1.closed {
		t.Errorf("Index 1 conn not closed by StopPingRoute(Index 1)")
	}
	if _, ok := v.ping.conns[PingRouteRef{PK: pk, Index: 0}]; !ok {
		t.Errorf("Index 0 removed from map by StopPingRoute(Index 1)")
	}
	if _, ok := v.ping.conns[PingRouteRef{PK: pk, Index: 1}]; ok {
		t.Errorf("Index 1 still in map after StopPingRoute(Index 1)")
	}
}

// TestStopPingRouteIdempotent pins idempotency: cleanup paths that
// aren't sure whether a route was ever established should be safe
// to call StopPingRoute on a missing ref. Returns nil, no panic.
func TestStopPingRouteIdempotent(t *testing.T) {
	v := newPingStateVisor()
	pk := helperPK(6)

	// No conn ever stored.
	if err := v.StopPingRoute(PingRouteRef{PK: pk, Index: 7}); err != nil {
		t.Errorf("StopPingRoute on unknown ref should be nil error, got %v", err)
	}

	// Map entry exists but conn is nil (the close path tolerates this).
	v.ping.conns[PingRouteRef{PK: pk, Index: 0}] = ping{conn: nil}
	if err := v.StopPingRoute(PingRouteRef{PK: pk, Index: 0}); err != nil {
		t.Errorf("StopPingRoute on nil-conn entry should be nil error, got %v", err)
	}
}

// TestStopPingRoutePropagatesCloseError pins that conn-close errors
// surface to the caller — mux-bw's defer chain logs them, so
// swallowing would mask real issues.
func TestStopPingRoutePropagatesCloseError(t *testing.T) {
	v := newPingStateVisor()
	pk := helperPK(7)
	closeErr := errors.New("simulated close failure")
	v.ping.conns[PingRouteRef{PK: pk, Index: 0}] = ping{conn: &fakeConn{err: closeErr}}

	err := v.StopPingRoute(PingRouteRef{PK: pk, Index: 0})
	if !errors.Is(err, closeErr) {
		t.Errorf("StopPingRoute should return the conn.Close() error, got %v", err)
	}
	if _, ok := v.ping.conns[PingRouteRef{PK: pk, Index: 0}]; ok {
		t.Errorf("entry must still be deleted on close error")
	}
}

// TestGetPingRouteDetailsAtPerRoute pins per-route hop lookup —
// the consumer for MuxRouteEstablished.hops. Without this the
// peer-keyed GetPingRouteDetails(pk) returns whichever route was
// stored last, which is non-deterministic in the mux setup phase.
func TestGetPingRouteDetailsAtPerRoute(t *testing.T) {
	v := newPingStateVisor()
	pk := helperPK(8)

	hops0 := []router.RouteHopInfo{{TpID: "tp-a", From: "A", To: "B", TpType: "stcpr"}}
	hops1 := []router.RouteHopInfo{
		{TpID: "tp-b", From: "A", To: "X", TpType: "stcpr"},
		{TpID: "tp-c", From: "X", To: "B", TpType: "stcpr"},
	}
	v.ping.conns[PingRouteRef{PK: pk, Index: 0}] = ping{hopInfos: hops0}
	v.ping.conns[PingRouteRef{PK: pk, Index: 1}] = ping{hopInfos: hops1}

	got0 := v.GetPingRouteDetailsAt(PingRouteRef{PK: pk, Index: 0})
	if len(got0) != 1 || got0[0].TpID != "tp-a" {
		t.Errorf("route 0 hops mismatch: %+v", got0)
	}
	got1 := v.GetPingRouteDetailsAt(PingRouteRef{PK: pk, Index: 1})
	if len(got1) != 2 || got1[0].TpID != "tp-b" || got1[1].TpID != "tp-c" {
		t.Errorf("route 1 hops mismatch: %+v", got1)
	}
	if got := v.GetPingRouteDetailsAt(PingRouteRef{PK: pk, Index: 99}); got != nil {
		t.Errorf("unknown route should yield nil, got %+v", got)
	}
}
