// Package router pkg/router/route_group_policy_drop_test.go c2-net-routing
package router

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// TestPolicyDropKeepsTransportOpen checks a policy drop removes the leg and its
// rules from this group, leaves the transport, which other groups may ride,
// and tells the peer to retire the leg.
func TestPolicyDropKeepsTransportOpen(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 3)
	rg.mux.legStateEnabled = true
	dropped := mts[1]
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

	rg.dropLegs([]uuid.UUID{mts[1].Entry.ID})

	require.False(t, dropped.IsClosed(), "a policy drop must not close the transport")
	select {
	case <-retired:
	case <-time.After(5 * time.Second):
		t.Fatal("the peer was not told to retire the leg")
	}

	rg.mu.Lock()
	require.Len(t, rg.legs, 2)
	require.Len(t, testFwd(rg), 2)
	require.Len(t, testRvs(rg), 2)
	for _, tp := range legTransports(rg.legs) {
		require.NotEqual(t, dropped.Entry.ID, tp.Entry.ID, "the dropped leg must be gone")
	}
	rg.mu.Unlock()

	_, err := rg.rt.Rule(fwdID)
	require.Error(t, err, "the dropped leg's forward rule must be deleted")
	_, err = rg.rt.Rule(rvsID)
	require.Error(t, err, "the dropped leg's reverse rule must be deleted")
}

// TestPolicyDropClosesTransportForOldPeer checks the fallback for a peer that
// predates CloseLegRetired and would close the whole group on it.
func TestPolicyDropClosesTransportForOldPeer(t *testing.T) {
	rg, mts, conns := createMuxRouteGroup(t, 3)
	rg.mux.legStateEnabled = false
	conns[1].onWrite = func(b []byte) {
		if len(b) > routing.PacketTypeOffset && routing.Packet(b).Type() == routing.ClosePacket {
			t.Error("a close packet was sent to a peer that would close the group on it")
		}
	}

	rg.dropLegs([]uuid.UUID{mts[1].Entry.ID})

	require.True(t, mts[1].IsClosed(), "without the signal the peer learns of the drop from the close")
	rg.mu.Lock()
	require.Len(t, rg.legs, 2)
	rg.mu.Unlock()
}

// TestPolicyDropKeepsLastLeg checks the drop never empties the group.
func TestPolicyDropKeepsLastLeg(t *testing.T) {
	rg, mts, _ := createMuxRouteGroup(t, 2)

	rg.dropLegs([]uuid.UUID{mts[0].Entry.ID, mts[1].Entry.ID})

	rg.mu.Lock()
	defer rg.mu.Unlock()
	require.Len(t, rg.legs, 1)
	require.Equal(t, mts[1].Entry.ID, rg.legs[0].tp.Entry.ID)
}
