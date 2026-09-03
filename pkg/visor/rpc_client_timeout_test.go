// Package visor pkg/visor/rpc_client_timeout_test.go
package visor

import (
	"io"
	"net/rpc"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockedConn accepts writes and never answers reads — every RPC call
// through it hits the per-call deadline. Close unblocks readers.
type blockedConn struct {
	closed    chan struct{}
	closeOnce sync.Once
}

func newBlockedConn() *blockedConn {
	return &blockedConn{closed: make(chan struct{})}
}

func (c *blockedConn) Read(_ []byte) (int, error) {
	<-c.closed
	return 0, io.EOF
}

func (c *blockedConn) Write(p []byte) (int, error) {
	select {
	case <-c.closed:
		return 0, io.ErrClosedPipe
	default:
		return len(p), nil
	}
}

func (c *blockedConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *blockedConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

// TestRPCClientTimeoutTolerance verifies a single slow call no longer
// closes the shared conn: the hypervisor holds ONE persistent RPC conn
// per remote visor, and closing it on the first slow Summary (the old
// behavior) permanently shut down the net/rpc client — operators saw
// "connection is shut down (cached Ns ago)" across half the fleet
// during congestion. The conn must survive closeOnConsecTimeouts-1
// consecutive timeouts and close on the next one.
func TestRPCClientTimeoutTolerance(t *testing.T) {
	conn := newBlockedConn()
	rc, ok := NewRPCClient(nil, conn, RPCPrefix, 50*time.Millisecond).(*rpcClient)
	require.True(t, ok)

	for i := 1; i < closeOnConsecTimeouts; i++ {
		err := rc.Call("Health", &struct{}{}, &HealthInfo{})
		require.Error(t, err)
		assert.False(t, conn.isClosed(), "conn must survive %d consecutive timeouts", i)
	}

	err := rc.Call("Health", &struct{}{}, &HealthInfo{})
	require.Error(t, err)
	assert.True(t, conn.isClosed(), "conn must close after %d consecutive timeouts", closeOnConsecTimeouts)
}

// TestRPCClientTimeoutReset verifies the consecutive-timeout count
// resets on any COMPLETED call — success or a server-side error reply
// (both prove the conn round-trips) — but not on transport-level
// failures like rpc.ErrShutdown.
func TestRPCClientTimeoutReset(t *testing.T) {
	conn := newBlockedConn()
	rc, ok := NewRPCClient(nil, conn, RPCPrefix, 50*time.Millisecond).(*rpcClient)
	require.True(t, ok)

	rc.consecTimeouts.Store(closeOnConsecTimeouts - 1)
	rc.noteCallCompleted(rpc.ErrShutdown)
	assert.Equal(t, int32(closeOnConsecTimeouts-1), rc.consecTimeouts.Load(),
		"transport failure must not reset the count")

	rc.noteCallCompleted(rpc.ServerError("remote says no"))
	assert.Equal(t, int32(0), rc.consecTimeouts.Load(),
		"server-side error reply proves liveness and must reset the count")

	rc.consecTimeouts.Store(closeOnConsecTimeouts - 1)
	rc.noteCallCompleted(nil)
	assert.Equal(t, int32(0), rc.consecTimeouts.Load(),
		"success must reset the count")

	assert.False(t, conn.isClosed())
}
