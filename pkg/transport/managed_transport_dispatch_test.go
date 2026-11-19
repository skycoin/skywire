// Package transport pkg/transport/managed_transport_dispatch_test.go c2-net-transport
package transport

import (
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func servePipe(t *testing.T, readCh chan routing.Packet) (*ManagedTransport, net.Conn) {
	t.Helper()
	tm := newTestManager(t)
	remote := mustPK(t)
	mt := NewManagedTransport(ManagedTransportConfig{
		client:   &fakeClient{pk: tm.Conf.PubKey, sk: tm.Conf.SecKey, typ: types.STCPR},
		DC:       NewDiscoveryMock(),
		LS:       InMemoryTransportLogStore(),
		RemotePK: remote,
	})
	mt.queueDeletion = func(uuid.UUID) {} // no TPD round-trip in a unit test
	near, far := net.Pipe()
	mt.setTransport(&pipeTransport{Conn: near, lpk: tm.Conf.PubKey, rpk: remote, nw: types.STCPR})
	go mt.Serve(readCh)
	return mt, far
}

func dataPacket(t *testing.T, i int) routing.Packet {
	t.Helper()
	p, err := routing.MakeDataPacket(routing.RouteID(i+1), []byte{byte(i)}) //nolint:gosec // small test index
	require.NoError(t, err)
	return p
}

// Every packet read before the peer closes reaches readCh, in the order read.
func TestReadLoopDispatchKeepsOrder(t *testing.T) {
	const n = 500
	readCh := make(chan routing.Packet, n)
	mt, far := servePipe(t, readCh)

	for i := range n {
		_, err := far.Write(dataPacket(t, i))
		require.NoError(t, err)
	}
	require.NoError(t, far.Close())

	for i := range n {
		select {
		case got := <-readCh:
			require.Equal(t, routing.RouteID(i+1), got.RouteID(), "packet %d out of order", i)
		case <-time.After(3 * time.Second):
			t.Fatalf("packet %d never reached readCh", i)
		}
	}
	require.NoError(t, mt.Close())
}

// With readCh full the reader stops reading once the dispatch queue fills.
func TestReadLoopDispatchBackpressure(t *testing.T) {
	readCh := make(chan routing.Packet) // nobody reads
	mt, far := servePipe(t, readCh)

	var written atomic.Int32
	go func() {
		for i := range 4 * dispatchQueueLen {
			if _, err := far.Write(dataPacket(t, i)); err != nil {
				return
			}
			written.Add(1)
		}
	}()
	time.Sleep(300 * time.Millisecond)
	// The queue, one packet held by the dispatcher and one by the reader.
	require.LessOrEqual(t, int(written.Load()), dispatchQueueLen+2)

	require.NoError(t, mt.Close())
	_ = far.Close() //nolint:errcheck
}

// Closing the transport ends both the reader and the dispatcher, even while
// the dispatcher waits on a full readCh.
func TestReadLoopDispatchNoLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for range 20 {
		readCh := make(chan routing.Packet)
		mt, far := servePipe(t, readCh)
		for i := range 3 {
			_, err := far.Write(dataPacket(t, i))
			require.NoError(t, err)
		}
		done := make(chan struct{})
		go func() { _ = mt.Close(); close(done) }() //nolint:errcheck
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("Close did not return: a read or dispatch goroutine is stuck")
		}
		_ = far.Close() //nolint:errcheck
	}
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= before+2 },
		3*time.Second, 20*time.Millisecond, "goroutines leaked: before %d, now %d", before, runtime.NumGoroutine())
}
