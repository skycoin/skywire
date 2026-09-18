// Package router pkg/router/transport_close_test.go c2-net-routing
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestHandleTransportClosedPrunesTheLeg: a closed transport under one leg of a
// three-leg group takes that leg with it, at the close, and leaves the rest.
func TestHandleTransportClosedPrunesTheLeg(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 3)
	dead := mts[1].Entry.ID

	closeManagedTransport(mts[1], conns[1])
	require.Equal(t, 1, rg.handleTransportClosed(dead))

	rg.mu.Lock()
	defer rg.mu.Unlock()
	require.Len(t, rg.tps, 2, "the leg on the closed transport is gone")
	for _, tp := range rg.tps {
		require.NotEqual(t, dead, tp.Entry.ID)
	}
	require.False(t, rg.isClosed(), "a group with surviving legs stays open")
}

// TestHandleTransportClosedClosesTheSoleLegGroup: a one-leg group has nowhere
// to put the next byte once its transport is gone, so it closes instead of
// waiting for something above to time out. Every tunnel in the standby pool is
// a one-leg group, and this close is what the pool fails over on.
func TestHandleTransportClosedClosesTheSoleLegGroup(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 1)
	dead := mts[0].Entry.ID

	closeManagedTransport(mts[0], conns[0])
	require.Equal(t, 1, rg.handleTransportClosed(dead))

	require.Eventually(t, rg.isClosed, 2*time.Second, 5*time.Millisecond,
		"the group outlived the only transport it had")
}

// TestHandleTransportClosedIgnoresAnotherGroupsTransport keeps the hook from
// touching groups that never rode the transport — it is fired for every close
// on the visor, and most of them are nothing to do with any given group.
func TestHandleTransportClosedIgnoresAnotherGroupsTransport(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 2)
	require.Equal(t, 0, rg.handleTransportClosed(uuid.New()))
	rg.mu.Lock()
	defer rg.mu.Unlock()
	require.Len(t, rg.tps, 2)
}

// TestRouterCloseLegsOnTransportReachesEveryGroup: the router's hook is what
// the transport manager calls, and it must reach both the noise-wrapped groups
// and the raw ones still finishing their handshake.
func TestRouterCloseLegsOnTransportReachesEveryGroup(t *testing.T) {
	ns, nsMts, nsConns := createMuxRouteGroup(t, 3)
	raw, rawMts, rawConns := createMuxRouteGroup(t, 3)

	r := &router{
		logger: logging.MustGetLogger("transport_close_test"),
		rt:     ns.rt,
		rgsNs:  map[routing.RouteDescriptor]*NoiseRouteGroup{ns.desc: {rg: ns, Conn: ns}},
		rgsRaw: map[routing.RouteDescriptor]*RouteGroup{raw.desc: raw},
	}

	// Both groups happen to ride the same transport id — the shape a visor
	// with one link to a busy first hop actually has.
	dead := nsMts[0].Entry.ID
	rawMts[0].Entry.ID = dead
	closeManagedTransport(nsMts[0], nsConns[0])
	closeManagedTransport(rawMts[0], rawConns[0])

	require.Equal(t, 2, r.ruleOnTransportClosed(dead), "one leg ruled on in each group")

	ns.mu.Lock()
	require.Len(t, ns.tps, 2)
	ns.mu.Unlock()
	raw.mu.Lock()
	require.Len(t, raw.tps, 2)
	raw.mu.Unlock()
}

// TestCloseLegsOnTransportDoesNotRuleInline is the deadlock guard: the hook
// fires from inside ManagedTransport.WritePacket's own close path, and
// sendKeepAlive writes with rg.mu held, so the hook must never take rg.mu on
// the caller's goroutine.
func TestCloseLegsOnTransportDoesNotRuleInline(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 2)
	r := &router{
		logger: logging.MustGetLogger("transport_close_inline_test"),
		rt:     rg.rt,
		rgsNs:  map[routing.RouteDescriptor]*NoiseRouteGroup{rg.desc: {rg: rg, Conn: rg}},
		rgsRaw: make(map[routing.RouteDescriptor]*RouteGroup),
	}
	dead := mts[0].Entry.ID
	closeManagedTransport(mts[0], conns[0])

	// rg.mu held, exactly as sendKeepAlive holds it across its write.
	rg.mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.closeLegsOnTransport(dead)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		rg.mu.Unlock()
		t.Fatal("the close hook blocked on rg.mu — it must rule off the caller's goroutine")
	}
	rg.mu.Unlock()

	require.Eventually(t, func() bool {
		rg.mu.Lock()
		defer rg.mu.Unlock()
		return len(rg.tps) == 1
	}, 2*time.Second, 5*time.Millisecond, "the leg on the closed transport was never pruned")
}
