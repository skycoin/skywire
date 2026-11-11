// Package router pkg/router/pool_release_close_test.go c2-net-routing
package router

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// TestPoolReleaseKeepsTransportOpen checks the release that cannot split drops
// the leg and its rules, leaves the transport other groups may ride, and tells
// the peer to retire the leg.
func TestPoolReleaseKeepsTransportOpen(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 3)
	rg.muxEvents = &muxEventRing{}
	rg.mux.legStateEnabled = true
	rg.mu.Lock()
	fwdID, rvsID := rg.legs[1].fwd.KeyRouteID(), rg.legs[1].rvs.KeyRouteID()
	retire := routing.MakeClosePacket(rg.legs[1].fwd.NextRouteID(), routing.CloseLegRetired)
	rg.mu.Unlock()
	retired := make(chan struct{}, 1)
	conns[1].onWrite = func(b []byte) {
		if bytes.Contains(b, retire) {
			select {
			case retired <- struct{}{}:
			default:
			}
		}
	}

	rg.closeReleasedLeg(mts[1].Entry.ID, "test")

	require.False(t, mts[1].IsClosed(), "a pool release must not close the transport")
	select {
	case <-retired:
	case <-time.After(5 * time.Second):
		t.Fatal("the peer was not told to retire the leg")
	}
	require.Equal(t, -1, legIdxOf(rg, mts[1]), "the released leg must be gone")
	require.Equal(t, 1, countEvents(rg, MuxEventPoolLegReleased))
	_, err := rg.rt.Rule(fwdID)
	require.Error(t, err, "the released leg's forward rule must be deleted")
	_, err = rg.rt.Rule(rvsID)
	require.Error(t, err, "the released leg's reverse rule must be deleted")
}

// TestPoolReleaseClosesTransportForOldPeer checks the fallback for a peer that
// predates CloseLegRetired and would close the whole group on it.
func TestPoolReleaseClosesTransportForOldPeer(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 3)
	rg.mux.legStateEnabled = false
	conns[1].onWrite = func(b []byte) {
		if len(b) > routing.PacketTypeOffset && routing.Packet(b).Type() == routing.ClosePacket {
			t.Error("a close packet was sent to a peer that would close the group on it")
		}
	}

	rg.closeReleasedLeg(mts[1].Entry.ID, "test")

	require.True(t, mts[1].IsClosed(), "without the signal the peer learns of the release from the close")
	require.Equal(t, -1, legIdxOf(rg, mts[1]))
}

// TestPoolReleaseKeepsLastLeg checks the release never empties the group.
func TestPoolReleaseKeepsLastLeg(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 1)
	rg.mux.legStateEnabled = true

	rg.closeReleasedLeg(mts[0].Entry.ID, "test")

	require.Equal(t, 0, legIdxOf(rg, mts[0]))
	require.False(t, mts[0].IsClosed())
}
