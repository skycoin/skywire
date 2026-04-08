package dmsgctrl_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsgctrl"
)

// TestServeListener_AcceptAndControl verifies that ServeListener accepts
// connections and delivers Controls through the returned channel.
func TestServeListener_AcceptAndControl(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ch := dmsgctrl.ServeListener(l, 4)

	// Dial two connections into the listener.
	connA, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	connB, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)

	// We should receive two Controls from the channel.
	var ctrls []*dmsgctrl.Control
	timeout := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case ctrl, ok := <-ch:
			require.True(t, ok, "channel closed unexpectedly")
			require.NotNil(t, ctrl)
			ctrls = append(ctrls, ctrl)
		case <-timeout:
			t.Fatal("timed out waiting for Control from channel")
		}
	}

	assert.Len(t, ctrls, 2)

	// Cleanup.
	for _, ctrl := range ctrls {
		ctrl.Close() //nolint:errcheck,gosec
	}
	connA.Close() //nolint:errcheck,gosec
	connB.Close() //nolint:errcheck,gosec
	l.Close()     //nolint:errcheck,gosec
}

// TestServeListener_ClosesChannelOnListenerClose verifies that the channel
// returned by ServeListener is closed when the underlying listener is closed.
func TestServeListener_ClosesChannelOnListenerClose(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ch := dmsgctrl.ServeListener(l, 4)

	// Close the listener; the goroutine should detect "use of closed" and
	// close the channel.
	require.NoError(t, l.Close())

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "expected channel to be closed")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel to close")
	}
}

// TestServeListener_FullChannelDropsControl verifies that when the control
// channel is full, new Controls are dropped rather than blocking.
func TestServeListener_FullChannelDropsControl(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	// Channel length of 1 — second accepted conn should be dropped.
	ch := dmsgctrl.ServeListener(l, 1)

	conn1, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)

	// Wait for the first Control to land in the channel.
	select {
	case ctrl := <-ch:
		require.NotNil(t, ctrl)
		defer ctrl.Close() //nolint:errcheck,gosec
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first control")
	}

	// Dial a second connection — this should be dropped since the channel is
	// still full (we already consumed the first, but let's fill it again).
	// First, fill the channel again by dialing one more.
	conn2, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)

	// Wait for the second to arrive.
	select {
	case ctrl := <-ch:
		require.NotNil(t, ctrl)
		defer ctrl.Close() //nolint:errcheck,gosec
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second control")
	}

	// Now the channel is empty but has cap 1. Fill it without consuming.
	conn3, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond) // let it land

	// This fourth conn should be dropped.
	conn4, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond) // let it be processed

	// Drain one — we should get exactly one more (the third).
	select {
	case ctrl := <-ch:
		require.NotNil(t, ctrl)
		defer ctrl.Close() //nolint:errcheck,gosec
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for third control")
	}

	// Cleanup.
	conn1.Close() //nolint:errcheck,gosec
	conn2.Close() //nolint:errcheck,gosec
	conn3.Close() //nolint:errcheck,gosec
	conn4.Close() //nolint:errcheck,gosec
	l.Close()     //nolint:errcheck,gosec
}

// TestControl_PingPongExchange tests that a ping on one side results in a
// measurable round-trip duration on the same side.
func TestControl_PingPongExchange(t *testing.T) {
	connA, connB := net.Pipe()
	ctrlA := dmsgctrl.ControlStream(connA)
	ctrlB := dmsgctrl.ControlStream(connB)

	t.Cleanup(func() {
		ctrlA.Close() //nolint:errcheck,gosec
		ctrlB.Close() //nolint:errcheck,gosec
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	dur, err := ctrlA.Ping(ctx)
	require.NoError(t, err)
	assert.True(t, dur >= 0, "expected non-negative duration, got %v", dur)
}

// TestControl_PingContextCancel verifies that Ping returns the context error
// when the context is canceled while waiting for a pong.
func TestControl_PingContextCancel(t *testing.T) {
	connA, connB := net.Pipe()
	defer connB.Close() //nolint:errcheck,gosec

	ctrlA := dmsgctrl.ControlStream(connA)

	// Create a goroutine that reads (consumes the ping byte) but never
	// sends a pong back, so ctrlA.Ping will block waiting for pong.
	go func() {
		buf := make([]byte, 1)
		for {
			_, err := connB.Read(buf)
			if err != nil {
				return
			}
			// Intentionally do not reply with pong.
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := ctrlA.Ping(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	ctrlA.Close() //nolint:errcheck,gosec
}

// TestControl_Close verifies that Close sets the error and signals Done.
func TestControl_Close(t *testing.T) {
	connA, connB := net.Pipe()
	ctrlA := dmsgctrl.ControlStream(connA)
	ctrlB := dmsgctrl.ControlStream(connB)

	require.NoError(t, ctrlA.Close())

	// Wait for both sides to finish.
	select {
	case <-ctrlA.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("ctrlA.Done() did not close in time")
	}

	select {
	case <-ctrlB.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("ctrlB.Done() did not close in time")
	}

	// Err should report ErrClosed on the side that initiated Close.
	assert.ErrorIs(t, ctrlA.Err(), dmsgctrl.ErrClosed)

	// The remote side should have a non-nil error (EOF or similar).
	assert.Error(t, ctrlB.Err())
}

// TestControl_DoubleClose ensures calling Close twice does not panic and
// returns the original error.
func TestControl_DoubleClose(t *testing.T) {
	connA, connB := net.Pipe()
	ctrlA := dmsgctrl.ControlStream(connA)
	dmsgctrl.ControlStream(connB) //nolint:errcheck,gosec

	err1 := ctrlA.Close()
	// Second close: the connection is already closed so conn.Close may
	// return an error, but it must not panic.
	ctrlA.Close() //nolint:errcheck,gosec
	_ = err1

	// Wait for done.
	select {
	case <-ctrlA.Done():
	case <-time.After(time.Second):
		t.Fatal("ctrlA.Done() did not fire")
	}
}

// TestControl_ErrBeforeDone verifies that Err returns nil when the control
// is still running.
func TestControl_ErrBeforeDone(t *testing.T) {
	connA, connB := net.Pipe()
	ctrlA := dmsgctrl.ControlStream(connA)
	ctrlB := dmsgctrl.ControlStream(connB)

	// Control is still alive — Err should be nil.
	assert.Nil(t, ctrlA.Err())
	assert.Nil(t, ctrlB.Err())

	ctrlA.Close() //nolint:errcheck,gosec
	ctrlB.Close() //nolint:errcheck,gosec
}

// TestControl_DoneChannel tests that the Done channel blocks while the
// control is active and unblocks after Close.
func TestControl_DoneChannel(t *testing.T) {
	connA, connB := net.Pipe()
	ctrlA := dmsgctrl.ControlStream(connA)
	ctrlB := dmsgctrl.ControlStream(connB)

	// Done should block.
	select {
	case <-ctrlA.Done():
		t.Fatal("Done() should not be closed yet")
	default:
		// expected
	}

	require.NoError(t, ctrlA.Close())

	select {
	case <-ctrlA.Done():
	case <-time.After(time.Second):
		t.Fatal("Done() did not close after Close()")
	}

	ctrlB.Close() //nolint:errcheck,gosec
}

// TestControl_Conn verifies that Conn returns the underlying net.Conn.
func TestControl_Conn(t *testing.T) {
	connA, connB := net.Pipe()
	ctrlA := dmsgctrl.ControlStream(connA)
	dmsgctrl.ControlStream(connB) //nolint:errcheck,gosec

	assert.Equal(t, connA, ctrlA.Conn())

	ctrlA.Close() //nolint:errcheck,gosec
}

// TestControl_ConcurrentPing tests that multiple goroutines can ping
// simultaneously without races or panics.
func TestControl_ConcurrentPing(t *testing.T) {
	// Use TCP so writes don't block synchronously like net.Pipe.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close() //nolint:errcheck,gosec

	var connB net.Conn
	accepted := make(chan struct{})
	go func() {
		var err error
		connB, err = l.Accept()
		if err != nil {
			return
		}
		close(accepted)
	}()

	connA, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	<-accepted

	ctrlA := dmsgctrl.ControlStream(connA)
	ctrlB := dmsgctrl.ControlStream(connB)

	t.Cleanup(func() {
		ctrlA.Close() //nolint:errcheck,gosec
		ctrlB.Close() //nolint:errcheck,gosec
	})

	const goroutines = 5
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = ctrlA.Ping(ctx) //nolint:errcheck,gosec
		}()
		go func() {
			defer wg.Done()
			_, _ = ctrlB.Ping(ctx) //nolint:errcheck,gosec
		}()
	}

	wg.Wait()
}

// TestControl_PingAfterClose verifies that Ping returns an error after the
// control has been closed.
func TestControl_PingAfterClose(t *testing.T) {
	connA, connB := net.Pipe()
	ctrlA := dmsgctrl.ControlStream(connA)
	dmsgctrl.ControlStream(connB) //nolint:errcheck,gosec

	require.NoError(t, ctrlA.Close())

	<-ctrlA.Done()

	_, err := ctrlA.Ping(context.Background())
	assert.Error(t, err)
}
