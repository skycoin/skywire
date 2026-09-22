// Package router pkg/router/leg_rehome_test.go c2-net-routing
//
// Unit coverage for the two things a re-home does on its own: the rule rewrite
// (same key route ID, new descriptor — the chain keeps every reserved ID it
// holds at every hop) and the refusals that must leave both groups untouched.
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestRehomeConsumeRuleKeepsItsRouteID is the crux of leg re-home: the chain's
// inbound route ID is what every hop's intermediary rule points at, so the
// rewrite must change the DESCRIPTOR and nothing else.
func TestRehomeConsumeRuleKeepsItsRouteID(t *testing.T) {
	dst, src, _, _, _ := endpointsForTest(t)
	old := routing.ConsumeRule(3*time.Minute, 4242, src, dst, 1, 2)
	target := routing.NewRouteDescriptor(src, dst, 7, 8)

	got, err := rehomeConsumeRule(old, target)
	require.NoError(t, err)
	require.Equal(t, old.KeyRouteID(), got.KeyRouteID(), "the chain's reserved inbound ID must not move")
	require.Equal(t, routing.RuleReverse, got.Type())
	require.Equal(t, target, got.RouteDescriptor(), "the rewritten rule must select the adopting group")
	require.Equal(t, old.KeepAlive(), got.KeepAlive())

	// A forward rule is not a consume rule: refuse rather than mint a wrong one.
	_, err = rehomeConsumeRule(routing.ForwardRule(time.Minute, 1, 2, uuid.New(), src, dst, 1, 2), target)
	require.Error(t, err)
	_, err = rehomeConsumeRule(nil, target)
	require.Error(t, err)
}

// TestRehomeForwardRuleKeepsItsNextHop pins the send side: the forward rule's
// next route ID and transport ARE the chain, so only its descriptor changes.
func TestRehomeForwardRuleKeepsItsNextHop(t *testing.T) {
	dst, src, _, _, _ := endpointsForTest(t)
	tpID := uuid.New()
	old := routing.ForwardRule(3*time.Minute, 111, 222, tpID, src, dst, 1, 2)
	target := routing.NewRouteDescriptor(src, dst, 7, 8)

	got, err := rehomeForwardRule(old, target)
	require.NoError(t, err)
	require.Equal(t, routing.RouteID(111), got.KeyRouteID())
	require.Equal(t, routing.RouteID(222), got.NextRouteID(), "the next hop is the chain; it must not move")
	require.Equal(t, tpID, got.NextTransportID())
	require.Equal(t, target, got.RouteDescriptor())

	_, err = rehomeForwardRule(routing.ConsumeRule(time.Minute, 1, src, dst, 1, 2), target)
	require.Error(t, err)
}

// TestRehomeNonceIsUnique guards the request/ack/commit correlation: two
// re-homes in flight at once must not answer each other's waiter.
func TestRehomeNonceIsUnique(t *testing.T) {
	seen := make(map[uint64]bool, 64)
	for i := 0; i < 64; i++ {
		n, err := newRehomeNonce()
		require.NoError(t, err)
		require.False(t, seen[n], "re-home nonce repeated")
		seen[n] = true
	}
}

// TestRehomeRefusedWhenPeerLacksTheBit is the fallback contract: without a
// negotiated CapLegRehome the caller gets ErrRehomeUnsupported and can branch
// to the dial-based pool-sourced leg instead.
func TestRehomeRefusedWhenPeerLacksTheBit(t *testing.T) {
	dst, src, _, _, _ := endpointsForTest(t)
	g := newRehomeTestGroup(t, src, dst, 1, 2, true)
	s := newRehomeTestGroup(t, src, dst, 3, 4, false)
	require.ErrorIs(t, rehomeChain(g, s), ErrRehomeUnsupported)

	// And with the bit on both sides but different peers, it is refused too —
	// a chain can only move between groups that already join the same visors.
	other := newRehomeTestGroup(t, src, src, 5, 6, true)
	require.Error(t, rehomeChain(g, other))
}

// newRehomeTestGroup builds a legless mux route group — enough for the
// capability and peer checks, which all run before anything is touched.
func newRehomeTestGroup(t *testing.T, srcPK, dstPK cipher.PubKey, srcPort, dstPort routing.Port, rehome bool) *RouteGroup {
	t.Helper()
	l := logging.NewMasterLogger()
	l.SetLevel(logrus.PanicLevel)
	rt := routing.NewTable(l.PackageLogger("rehome-rt"))
	rg := NewRouteGroup(DefaultRouteGroupConfig(), rt, routing.NewRouteDescriptor(srcPK, dstPK, srcPort, dstPort), l)
	rg.mux = newRouteMux(l.PackageLogger("rehome-mux"), true)
	rg.mux.legRehomeEnabled = rehome
	rg.muxEvents = &muxEventRing{}
	return rg
}
