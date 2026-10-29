package transport

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func readFrames(t *testing.T, r io.Reader, n int) [][]byte {
	t.Helper()
	out := make([][]byte, 0, n)
	for len(out) < n {
		var h [4]byte
		_, err := io.ReadFull(r, h[:])
		require.NoError(t, err)
		p := make([]byte, binary.BigEndian.Uint32(h[:]))
		_, err = io.ReadFull(r, p)
		require.NoError(t, err)
		out = append(out, p)
	}
	return out
}

// TestConnectionQueueBackPressure verifies a stalled writer fills the queue
// to its old channel capacity, a further send waits and then times out, and
// once the peer reads every queued frame arrives in order.
func TestConnectionQueueBackPressure(t *testing.T) {
	a, b := net.Pipe()
	c := newConnection(a, false)
	defer c.Close()
	defer b.Close() //nolint:errcheck

	// Wait for the writer to take the first frame and block in Write, so
	// the queue below fills with nothing left to drain it.
	require.True(t, c.QueueFrame(Frame{Body: []byte("0")}))
	require.Eventually(t, func() bool { return c.queued() == 0 }, 2*time.Second, time.Millisecond)
	n := 1
	for c.QueueFrame(Frame{Body: []byte(fmt.Sprint(n))}) {
		n++
		require.Less(t, n, 10*sendQueueLen, "the queue must be bounded")
	}
	// The writer holds one frame and the queue the rest, as with a channel.
	require.Equal(t, sendQueueLen+1, n)

	start := time.Now()
	err := c.SendFrame(Frame{Body: []byte("late")}, nil, 100*time.Millisecond)
	require.ErrorIs(t, err, ErrSendTimeout)
	require.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond)

	stop := make(chan struct{})
	close(stop)
	require.ErrorIs(t, c.SendFrame(Frame{Body: []byte("late")}, stop, time.Minute), ErrSendStopped)

	sent := make(chan error, 1)
	go func() { sent <- c.SendFrame(Frame{Body: []byte(fmt.Sprint(n))}, nil, 5*time.Second) }()
	got := readFrames(t, b, n+1)
	require.NoError(t, <-sent)
	for i, p := range got {
		require.Equal(t, fmt.Sprint(i), string(p))
	}
	require.Eventually(t, func() bool { return !c.writing.Load() }, 2*time.Second, time.Millisecond)
	c.qmu.Lock()
	require.Nil(t, c.q, "a drained queue holds no buffer")
	c.qmu.Unlock()
}

// TestConnectionQueueConcurrentSenders verifies senders waiting on a full
// queue all get through, with no frame lost and each sender's order kept.
func TestConnectionQueueConcurrentSenders(t *testing.T) {
	a, b := net.Pipe()
	c := newConnection(a, false)
	defer c.Close()
	defer b.Close() //nolint:errcheck

	const senders, each = 8, 200
	var wg sync.WaitGroup
	for s := 0; s < senders; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := c.SendFrame(Frame{Body: []byte{byte(s), byte(i >> 8), byte(i)}}, nil, 10*time.Second); err != nil { //nolint:gosec
					t.Error(err)
					return
				}
			}
		}(s)
	}
	time.Sleep(50 * time.Millisecond) // let the queue fill before reading
	got := readFrames(t, b, senders*each)
	wg.Wait()
	next := make([]int, senders)
	for _, p := range got {
		s, i := int(p[0]), int(p[1])<<8|int(p[2])
		require.Equal(t, next[s], i, "sender %d out of order", s)
		next[s]++
	}
}
