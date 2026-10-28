package transport

import (
	"encoding/binary"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func frameN(i int) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(i)) //nolint:gosec
	return b
}

// deliver still back-pressures at vstreamReadBuf frames: the next frame
// waits for the reader instead of being dropped, and every frame reaches the
// reader in order.
func TestVStreamQueueBackPressure(t *testing.T) {
	tm := newTestManager(t)
	mt, _ := servingTransport(t, tm, mustPK(t), types.STCPR)
	mux := NewVStreamMux(tm, routing.AppDirectPacket, logging.MustGetLogger("vstream-queue-test"))
	s, err := mux.DialOnTransportAs(mt, "test")
	require.NoError(t, err)
	defer s.Close() //nolint:errcheck

	for i := 0; i < vstreamReadBuf; i++ {
		mux.deliver(s, frameN(i))
	}
	require.Equal(t, vstreamReadBuf, s.rq.len())

	const extra = 3000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := vstreamReadBuf; i < vstreamReadBuf+extra; i++ {
			mux.deliver(s, frameN(i))
		}
	}()
	select {
	case <-done:
		t.Fatal("deliver did not wait on a full queue")
	case <-time.After(100 * time.Millisecond):
	}
	require.Equal(t, vstreamReadBuf, s.rq.len(), "a full queue takes no more")

	buf := make([]byte, 4)
	for i := 0; i < vstreamReadBuf+extra; i++ {
		_, err := io.ReadFull(s, buf)
		require.NoError(t, err)
		require.Equal(t, uint32(i), binary.BigEndian.Uint32(buf), "frame %d", i) //nolint:gosec
	}
	<-done
	require.Zero(t, s.rq.len())
	require.Zero(t, mux.Stats().StalledStreams)
	s.rq.mu.Lock()
	require.LessOrEqual(t, cap(s.rq.q), frameKeep, "a drained queue drops a large buffer")
	s.rq.mu.Unlock()
}

// A reader that takes nothing for vstreamStallTimeout still gets its stream
// closed, and the blocked deliver returns.
func TestVStreamQueueStallCloses(t *testing.T) {
	old := vstreamStallTimeout
	vstreamStallTimeout = 50 * time.Millisecond
	defer func() { vstreamStallTimeout = old }()
	tm := newTestManager(t)
	mt, _ := servingTransport(t, tm, mustPK(t), types.STCPR)
	mux := NewVStreamMux(tm, routing.AppDirectPacket, logging.MustGetLogger("vstream-queue-test"))
	s, err := mux.DialOnTransportAs(mt, "test")
	require.NoError(t, err)

	for i := 0; i <= vstreamReadBuf; i++ {
		mux.deliver(s, frameN(i))
	}
	require.Equal(t, int64(1), mux.Stats().StalledStreams)
	select {
	case <-s.closed:
	default:
		t.Fatal("a stalled stream must be closed")
	}
}
