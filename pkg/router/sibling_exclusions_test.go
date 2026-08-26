package router

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// seedSiblingRG registers a live NoiseRouteGroup from this visor (lPK) to a
// remote exit (rPK:rPort) that leaves over first-hop transport tpID toward
// intermediate midPK. rgsNs is keyed by the RECEIVE-side descriptor, so
// Src = remote peer / Dst = this visor — mirroring how DialRoutes' siblings
// are actually stored (router_mux_ops.go treats desc.SrcPK() as the peer).
func seedSiblingRG(t *testing.T, r *router, lPK, rPK, midPK cipher.PubKey, rPort, lPort routing.Port, tpID uuid.UUID) {
	t.Helper()
	desc := routing.NewRouteDescriptor(rPK, lPK, rPort, lPort)
	rg := NewRouteGroup(DefaultRouteGroupConfig(), r.rt, desc, logging.NewMasterLogger())

	mt := transport.NewManagedTransportForTest(nil)
	mt.Entry = transport.Entry{
		ID:    tpID,
		Type:  "test",
		Edges: [2]cipher.PubKey{lPK, midPK},
	}
	rg.mu.Lock()
	rg.tps = []*transport.ManagedTransport{mt}
	rg.mu.Unlock()

	r.rgsNs[desc] = &NoiseRouteGroup{rg: rg, Conn: rg}
}

func newExclusionTestRouter(t *testing.T, lPK cipher.PubKey) *router {
	t.Helper()
	l := logging.NewMasterLogger()
	return &router{
		logger: l.PackageLogger("router-diversify-test"),
		conf:   &Config{PubKey: lPK},
		rt:     routing.NewTable(l.PackageLogger("rt")),
		rgsNs:  make(map[routing.RouteDescriptor]*NoiseRouteGroup),
		rgsRaw: make(map[routing.RouteDescriptor]*RouteGroup),
	}
}

// TestSiblingRouteGroupExclusions_DiversifiesSecondTunnel proves the core of
// disjoint-tunnel dial coordination (docs/mux_aggregation_rfc.md step 3): a
// second dial to the SAME exit collects the first tunnel's first-hop transport
// + intermediate, so the router excludes them and the tunnels land on
// different first-hop transports (their throughputs then sum, not split).
func TestSiblingRouteGroupExclusions_DiversifiesSecondTunnel(t *testing.T) {
	lPK, _ := cipher.GenerateKeyPair()
	rPK, _ := cipher.GenerateKeyPair()
	midPK, _ := cipher.GenerateKeyPair()
	const rPort = routing.Port(3)

	r := newExclusionTestRouter(t, lPK)

	// Tunnel 1 is already live to the exit over transport tp1 via midPK.
	tp1 := uuid.New()
	seedSiblingRG(t, r, lPK, rPK, midPK, rPort, routing.Port(1001), tp1)

	// Tunnel 2 dialing the same exit must see tunnel 1 as a sibling and be
	// handed its transport + intermediate to exclude.
	ids, pks, count := r.siblingRouteGroupExclusions(lPK, rPK, rPort)
	require.Equal(t, 1, count, "tunnel 2 must see exactly one live sibling to the exit")
	require.Equal(t, []uuid.UUID{tp1}, ids, "tunnel 2 must exclude tunnel 1's first-hop transport")
	require.Equal(t, []cipher.PubKey{midPK}, pks, "tunnel 2 must exclude tunnel 1's intermediate")
}

// TestSiblingRouteGroupExclusions_LoneDialUntouched proves the bounding: the
// first tunnel to a dst (or any app that dials it once) finds no sibling, so
// no exclusions are added and the dial is byte-identical to today.
func TestSiblingRouteGroupExclusions_LoneDialUntouched(t *testing.T) {
	lPK, _ := cipher.GenerateKeyPair()
	rPK, _ := cipher.GenerateKeyPair()
	otherPK, _ := cipher.GenerateKeyPair()
	midPK, _ := cipher.GenerateKeyPair()
	const rPort = routing.Port(3)

	r := newExclusionTestRouter(t, lPK)

	// No route group to (rPK, rPort) at all → lone dial, no exclusions.
	ids, pks, count := r.siblingRouteGroupExclusions(lPK, rPK, rPort)
	require.Zero(t, count)
	require.Empty(t, ids)
	require.Empty(t, pks)

	// A live route group to a DIFFERENT exit (otherPK) must not be treated as
	// a sibling — diversification is scoped to the exact destination.
	seedSiblingRG(t, r, lPK, otherPK, midPK, rPort, routing.Port(1001), uuid.New())
	ids, pks, count = r.siblingRouteGroupExclusions(lPK, rPK, rPort)
	require.Zero(t, count, "a route group to another exit is not a sibling")
	require.Empty(t, ids)
	require.Empty(t, pks)

	// A route group to the right exit PK but a DIFFERENT port is not a sibling
	// either.
	seedSiblingRG(t, r, lPK, rPK, midPK, routing.Port(9), routing.Port(1002), uuid.New())
	_, _, count = r.siblingRouteGroupExclusions(lPK, rPK, rPort)
	require.Zero(t, count, "a route group to a different port on the exit is not a sibling")
}
