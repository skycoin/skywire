// Package router pkg/router/scenarios_leg_gone_emu_test.go c2-net-routing
package router

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// One end loses a leg's transport while the other end's transport for that
// leg stays up and silently drops what it sends. The end that lost it must
// tell the other at once, so the other stops striping into the dead path
// instead of waiting out three liveness probes.
func TestEmuLegGoneNoticePrunesThePeersLeg(t *testing.T) {
	sbdOffForEmu(t)
	g := newEmuRig(t, emuOpts{
		Legs: []emuLegSpec{
			symmetric("a-2MBs-60ms", 2*emuMB, 60*time.Millisecond, 2*emuMB),
			symmetric("b-2MBs-80ms", 2*emuMB, 80*time.Millisecond, 2*emuMB),
		},
		LegRehome: true,
	})
	src, dst := g.A.rg.desc.SrcPK(), g.A.rg.desc.DstPK()
	for _, e := range []*emuEnd{g.A, g.B} {
		e.rg.mu.Lock()
		for i := range e.rg.legs {
			e.rg.legs[i].hops = []routing.Hop{{TpID: g.ids[i], From: src, To: dst}}
		}
		e.rg.mu.Unlock()
	}
	before := globalMuxCounters.snapshot()

	dead := g.ids[0]
	g.Leg(0).Cut()
	g.A.tps[0].CloseForTest()
	require.Equal(t, 1, g.A.rg.handleTransportClosed(dead))

	require.Eventually(t, func() bool { return g.LegsOn(false) == 1 }, 2*time.Second, 10*time.Millisecond,
		"the acceptor kept the leg its peer reported dead")
	_, tp := g.B.rg.legByID(dead)
	require.Nil(t, tp, "the acceptor pruned the wrong leg")

	after := globalMuxCounters.snapshot()
	require.Positive(t, after.LegGoneSent-before.LegGoneSent)
	require.Positive(t, after.LegGonePruned-before.LegGonePruned)

	// The surviving leg still carries data both ways.
	for _, dir := range []emuDir{emuDown, emuUp} {
		x := g.Transfer(dir, 256*1024, 20*time.Second)
		require.NoError(t, x.Err, "%s after the prune", dir)
		require.True(t, x.HashOK, "%s after the prune", dir)
	}

	// A second notice for the same leg is a no-op.
	g.B.rg.handleLegGone(dead)
	require.Equal(t, 1, g.LegsOn(false))
	g.B.rg.handleLegGone(uuid.New())
	require.Equal(t, 1, g.LegsOn(false))
}

// A peer that predates the leg-gone kind decodes the frame as an unknown kind
// and drops it.
func TestLegGoneFrameIsAnUnknownKindToAnOldDecoder(t *testing.T) {
	id := uuid.New()
	body := routing.EncodeLegGoneFrame(id)
	kind, _, _, _, _, ok := routing.DecodeLegControlFrame(body)
	require.True(t, ok)
	require.NotEqual(t, routing.LegControlKindRehome, kind)

	got, ok := routing.DecodeLegGoneFrame(body)
	require.True(t, ok)
	require.Equal(t, id, got)

	rehome := routing.EncodeLegControlFrame(routing.LegControlKindRehome, 1, 2, 3, 0)
	_, ok = routing.DecodeLegGoneFrame(rehome)
	require.False(t, ok, "a re-home frame must not read as a leg-gone notice")
}

// The notice names the hop that touches the peer, whichever way the route was
// recorded.
func TestPeerFirstHopPicksTheHopTouchingThePeer(t *testing.T) {
	me, _ := cipher.GenerateKeyPair()
	relay, _ := cipher.GenerateKeyPair()
	peer, _ := cipher.GenerateKeyPair()
	first, second := uuid.New(), uuid.New()
	fwd := []routing.Hop{{TpID: first, From: me, To: relay}, {TpID: second, From: relay, To: peer}}
	require.Equal(t, second, peerFirstHop(fwd, peer))
	require.Equal(t, second, peerFirstHop(reverseHops(fwd), peer))
	require.Equal(t, uuid.Nil, peerFirstHop(nil, peer))
}
