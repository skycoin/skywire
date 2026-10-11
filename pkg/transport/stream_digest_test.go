package transport

import (
	"context"
	"math/rand"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/telemetrywire"
	"github.com/skycoin/skywire/pkg/transport/network"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

type fakeConn struct{ network.Transport }

// Two ends that see the same bytes in different write and read sizes reach
// the same checkpoints.
func TestStreamDigestMatchesAcrossChunking(t *testing.T) {
	data := make([]byte, 3*CheckpointBase+12345)
	rand.New(rand.NewSource(1)).Read(data) //nolint:gosec
	conn := &fakeConn{}
	var sender, receiver streamDigest
	sender.reset(conn)
	receiver.reset(conn)
	for p, step := data, 70000; len(p) > 0; {
		k := min(step, len(p))
		sender.add(conn, p[:k])
		p = p[k:]
	}
	for p, step := data, 1<<20+7; len(p) > 0; {
		k := min(step, len(p))
		receiver.add(conn, p[:k])
		p = p[k:]
	}
	s, r := sender.snapshot(), receiver.snapshot()
	require.Equal(t, s, r)
	require.NotZero(t, s.Epoch)
	require.EqualValues(t, 3*CheckpointBase, s.Latest[0].Offset)
	require.Zero(t, s.Latest[1].Offset, "256 MiB not reached")
}

// One different byte changes every later checkpoint, and bytes of another
// connection are not counted.
func TestStreamDigestDetectsDifference(t *testing.T) {
	data := make([]byte, 2*CheckpointBase)
	conn, old := &fakeConn{}, &fakeConn{}
	var a, b streamDigest
	a.reset(conn)
	b.reset(conn)
	a.add(conn, data)
	data[CheckpointBase+5] = 1
	b.add(old, []byte("written to the old conn"))
	b.add(conn, data)
	sa, sb := a.snapshot(), b.snapshot()
	require.Equal(t, sa.Epoch, sb.Epoch, "the first 16 MiB still match")
	require.NotEqual(t, sa.Latest[0].Digest, sb.Latest[0].Digest)
	require.EqualValues(t, 2*CheckpointBase, sb.Latest[0].Offset)
}

// A new connection starts a new stream.
func TestStreamDigestReset(t *testing.T) {
	conn := &fakeConn{}
	var d streamDigest
	d.reset(conn)
	d.add(conn, make([]byte, CheckpointBase))
	require.NotZero(t, d.snapshot().Epoch)
	next := &fakeConn{}
	d.reset(next)
	require.Equal(t, DigestState{}, d.snapshot())
	d.add(conn, make([]byte, CheckpointBase))
	require.Equal(t, DigestState{}, d.snapshot(), "the replaced conn no longer counts")
}

// What one transport sends and its peer receives over a real conn reach the
// same checkpoint, pings and data alike.
func TestTransportCheckpointsMatchPeer(t *testing.T) {
	tm := newTestManager(t)
	remote := mustPK(t)
	newMT := func() *ManagedTransport {
		dc := NewDiscoveryMock()
		mt := NewManagedTransport(ManagedTransportConfig{
			client:   &fakeClient{pk: tm.Conf.PubKey, sk: tm.Conf.SecKey, typ: types.STCPR},
			DC:       dc,
			LS:       InMemoryTransportLogStore(),
			RemotePK: remote,
		})
		// Registered, so closing deletes it at once instead of retrying.
		require.NoError(t, dc.RegisterTransports(context.Background(), &SignedEntry{Entry: &mt.Entry}))
		return mt
	}
	sender, receiver := newMT(), newMT()
	near, far := net.Pipe()
	sender.transportMx.Lock()
	sender.setTransport(&pipeTransport{Conn: near, lpk: tm.Conf.PubKey, rpk: remote, nw: types.STCPR})
	sender.transportMx.Unlock()
	receiver.transportMx.Lock()
	receiver.setTransport(&pipeTransport{Conn: far, lpk: remote, rpk: tm.Conf.PubKey, nw: types.STCPR})
	receiver.transportMx.Unlock()
	defer sender.close()
	defer receiver.close()

	readCh := make(chan routing.Packet, 64)
	go receiver.Serve(readCh)
	go func() {
		for range readCh { //nolint:revive // drain
		}
	}()

	payload := make([]byte, 60000)
	for sent := 0; sent < CheckpointBase+len(payload); sent += len(payload) {
		pkt, err := routing.MakeDataPacket(routing.RouteID(7), payload)
		require.NoError(t, err)
		require.NoError(t, sender.WritePacket(context.Background(), pkt))
	}
	require.Eventually(t, func() bool {
		_, recv := receiver.Checkpoints()
		return recv.Latest[0].Offset == CheckpointBase
	}, 10*time.Second, 20*time.Millisecond)
	sent, _ := sender.Checkpoints()
	_, recv := receiver.Checkpoints()
	require.Equal(t, sent.Latest[0], recv.Latest[0])
	require.Equal(t, sent.Epoch, recv.Epoch)
}

// The telemetry carries every level a transport keeps.
func TestCheckpointLevelsMatchWire(t *testing.T) {
	require.Equal(t, CheckpointLevels, telemetrywire.CheckpointLevels)
}
