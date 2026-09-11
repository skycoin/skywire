//go:build !tinygo || (js && wasm)

// Package router pkg/router/direct_setup_test.go c2-net-routing
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// The direct-setup gateway has no trusted-setup-node check to lean on: the
// transport peer is the authority, so these rules ARE the security boundary.
func TestDirectSetupGatewayValidate(t *testing.T) {
	localPK, _ := cipher.GenerateKeyPair()
	peerPK, _ := cipher.GenerateKeyPair()
	thirdPK, _ := cipher.GenerateKeyPair()

	ourTp := uuid.New()
	otherTp := uuid.New()

	gw := &directSetupGateway{
		logger: logging.MustGetLogger("direct-setup-test"),
		local:  localPK,
		peer:   peerPK,
		tpID:   ourTp,
	}

	const ka = time.Minute
	desc := routing.NewRouteDescriptor(localPK, peerPK, 1, 2)

	edge := func(fwd, rev routing.Rule, d routing.RouteDescriptor) routing.EdgeRules {
		return routing.EdgeRules{Desc: d, Forward: fwd, Reverse: rev}
	}
	goodFwd := routing.ForwardRule(ka, 1, 2, ourTp, localPK, peerPK, 1, 2)
	goodRev := routing.ConsumeRule(ka, 3, localPK, peerPK, 1, 2)

	t.Run("accepts a route between these two over this transport", func(t *testing.T) {
		require.NoError(t, gw.validate(edge(goodFwd, goodRev, desc)))
	})

	t.Run("accepts the descriptor in either direction", func(t *testing.T) {
		inverted := routing.NewRouteDescriptor(peerPK, localPK, 2, 1)
		require.NoError(t, gw.validate(edge(goodFwd, goodRev, inverted)))
	})

	t.Run("refuses a route this visor is not an endpoint of", func(t *testing.T) {
		foreign := routing.NewRouteDescriptor(peerPK, thirdPK, 1, 2)
		require.ErrorContains(t, gw.validate(edge(goodFwd, goodRev, foreign)),
			"does not name this visor and the requesting peer")
	})

	t.Run("refuses a route involving a third party", func(t *testing.T) {
		foreign := routing.NewRouteDescriptor(localPK, thirdPK, 1, 2)
		require.ErrorContains(t, gw.validate(edge(goodFwd, goodRev, foreign)),
			"does not name this visor and the requesting peer")
	})

	// The one that stops a peer using this visor as an unpaid transit hop:
	// without it, a forward rule aimed at a transport to someone else would
	// relay traffic onward without ever touching AddIntermediaryRules.
	t.Run("refuses a forward rule aimed at another transport", func(t *testing.T) {
		wrongTp := routing.ForwardRule(ka, 1, 2, otherTp, localPK, peerPK, 1, 2)
		require.ErrorContains(t, gw.validate(edge(wrongTp, goodRev, desc)),
			"is not the transport this request arrived on")
	})

	t.Run("refuses an intermediary rule dressed as an edge rule", func(t *testing.T) {
		inter := routing.IntermediaryForwardRule(ka, 1, 2, ourTp)
		require.ErrorContains(t, gw.validate(edge(inter, goodRev, desc)), "forward rule is")
	})

	t.Run("refuses a non-consume reverse rule", func(t *testing.T) {
		require.ErrorContains(t, gw.validate(edge(goodFwd, goodFwd, desc)), "reverse rule is")
	})

	t.Run("refuses missing rules", func(t *testing.T) {
		require.ErrorContains(t, gw.validate(edge(nil, goodRev, desc)), "must carry both")
		require.ErrorContains(t, gw.validate(edge(goodFwd, nil, desc)), "must carry both")
	})
}

func TestDirectSetupGatewayReserveIDsIsBounded(t *testing.T) {
	gw := &directSetupGateway{logger: logging.MustGetLogger("direct-setup-test")}
	var ids []routing.RouteID
	// A peer must not be able to drain this visor's route-ID space; the
	// bound is checked before the router is ever consulted, which is also
	// why a nil router does not panic here.
	require.Error(t, gw.ReserveIDs(0, &ids))
	require.Error(t, gw.ReserveIDs(5, &ids))
	require.Error(t, gw.ReserveIDs(255, &ids))
}

func TestStaticIDReserverPopsInOrder(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	c, _ := cipher.GenerateKeyPair()

	r := &staticIDReserver{ids: map[cipher.PubKey][]routing.RouteID{
		a: {1, 2},
		b: {10, 11},
	}}
	require.Equal(t, 4, r.TotalIDs())

	for _, want := range []routing.RouteID{1, 2} {
		got, ok := r.PopID(a)
		require.True(t, ok)
		require.Equal(t, want, got)
	}
	_, ok := r.PopID(a)
	require.False(t, ok, "exhausted stack must report failure, not a zero id")

	_, ok = r.PopID(c)
	require.False(t, ok, "unknown pk must report failure")

	got, ok := r.PopID(b)
	require.True(t, ok)
	require.Equal(t, routing.RouteID(10), got)
}
