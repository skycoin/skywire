// Package transport pkg/transport/managed_transport_swap_test.go c1-net-tp
package transport

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// A peer that re-dials lands on the same deterministic transport id, and
// setTransport swaps the underlying conn under the live ManagedTransport. The
// old conn's close surfaces in readLoop as a read error; it must NOT close the
// transport (and with it the just-installed conn) — the loop reads on.
func TestReadLoopSurvivesConnSwap(t *testing.T) {
	tm := newTestManager(t)
	remote := mustPK(t)
	fc := &fakeClient{pk: tm.Conf.PubKey, sk: tm.Conf.SecKey, typ: types.STCPR}
	mt := NewManagedTransport(ManagedTransportConfig{
		client:   fc,
		DC:       NewDiscoveryMock(),
		LS:       InMemoryTransportLogStore(),
		RemotePK: remote,
	})
	old := newBlockingTransport(tm.Conf.PubKey, remote, types.STCPR)
	mt.setTransport(old)
	tm.mx.Lock()
	tm.tps[mt.Entry.ID] = mt
	tm.mx.Unlock()
	tm.track(mt)

	readCh := make(chan routing.Packet, 8)
	go mt.Serve(readCh)
	time.Sleep(50 * time.Millisecond) // readLoop is now blocked in old.Read

	near, far := net.Pipe()
	defer far.Close() //nolint:errcheck
	newC := &pipeTransport{Conn: near, lpk: tm.Conf.PubKey, rpk: remote, nw: types.STCPR}
	mt.transportMx.Lock()
	mt.setTransport(newC) // closes old → its Read returns EOF in readLoop
	mt.transportMx.Unlock()

	pkt, err := routing.MakeDataPacket(routing.RouteID(7), []byte("after the swap"))
	require.NoError(t, err)
	go func() { _, _ = far.Write(pkt) }()

	select {
	case got := <-readCh:
		require.Equal(t, pkt, got)
	case <-time.After(3 * time.Second):
		t.Fatal("a packet on the new conn never reached readCh: readLoop died with the old conn")
	}
	require.False(t, mt.IsClosed(), "the transport must survive the conn swap")
	for _, ev := range tm.Events() {
		require.NotEqual(t, "close", ev.Event, "no close event may be recorded for a swapped transport: %+v", ev)
	}
	mt.close()
}
