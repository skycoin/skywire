package visor

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport"
)

// TestDialWithinLateFailedDialLeavesTypedNilAlone is the crash of 2026-11-25:
// a skynet call back dial that failed after dialBackTimeout handed dialWithin
// a typed nil *VStream, and the late close ran Close on it in a bare goroutine.
func TestDialWithinLateFailedDialLeavesTypedNilAlone(t *testing.T) {
	release := make(chan struct{})
	returned := make(chan struct{})
	dial := func(context.Context) (io.ReadWriteCloser, error) {
		<-release
		defer close(returned)
		var stream *transport.VStream
		return stream, errors.New("vstream: send SYN: io: read/write on closed pipe")
	}
	_, err := dialWithin(context.Background(), 10*time.Millisecond, dial)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
	<-returned
	// The late close runs on its own goroutine; give it time to crash.
	time.Sleep(100 * time.Millisecond)
}

// TestDialWithinClosesLateConn checks a conn that opens after the timeout is
// still closed, so the fix above did not leak it.
func TestDialWithinClosesLateConn(t *testing.T) {
	release := make(chan struct{})
	conn := &closeCountingConn{closed: make(chan struct{})}
	dial := func(context.Context) (io.ReadWriteCloser, error) {
		<-release
		return conn, nil
	}
	_, err := dialWithin(context.Background(), 10*time.Millisecond, dial)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
	select {
	case <-conn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("late conn was not closed")
	}
	require.EqualValues(t, 1, conn.closes.Load())
}

type closeCountingConn struct {
	closes atomic.Int32
	closed chan struct{}
}

func (c *closeCountingConn) Read([]byte) (int, error)  { return 0, io.EOF }
func (c *closeCountingConn) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (c *closeCountingConn) Close() error {
	if c.closes.Add(1) == 1 {
		close(c.closed)
	}
	return nil
}
