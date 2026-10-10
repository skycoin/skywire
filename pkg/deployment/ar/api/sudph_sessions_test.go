package api

import (
	"fmt"
	"net"
	"runtime"
	"testing"
	"time"

	kcp "github.com/0magnet/kcp-go/v5"
	"github.com/stretchr/testify/require"
)

func TestSudphSessionsCountsAndFrees(t *testing.T) {
	a := newTestAPI(t)
	l, err := kcp.Listen("127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close() //nolint:errcheck
	go a.ListenUDP(l)

	c, err := kcp.Dial(l.Addr().String())
	require.NoError(t, err)
	_, err = c.Write([]byte("not a handshake"))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return a.sudphSessionsReport(0).Accepted == 1
	}, 5*time.Second, 50*time.Millisecond)
	require.NoError(t, c.Close())

	require.Eventually(t, func() bool {
		runtime.GC()
		r := a.sudphSessionsReport(0)
		return r.Closed == 1 && r.InMemory == 0
	}, 3*sudphHandshakeTimeout, 200*time.Millisecond)
}

// The report reads the KCP listener's own session map, under its lock.
func TestSudphSessionsListenerMap(t *testing.T) {
	a := newTestAPI(t)
	l, err := kcp.Listen("127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close() //nolint:errcheck
	go a.ListenUDP(l)

	c, err := kcp.Dial(l.Addr().String())
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck
	_, err = c.Write([]byte("x"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return a.sudphSessionsReport(0).ListenerSessions >= 1
	}, 5*time.Second, 50*time.Millisecond)
}

// ?closed=N lists closed sessions that are still reachable, by address.
func TestSudphSessionsListsClosedAddrs(t *testing.T) {
	a := newTestAPI(t)
	l, err := kcp.Listen("127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close() //nolint:errcheck
	go a.ListenUDP(l)

	c, err := kcp.Dial(l.Addr().String())
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck
	_, err = c.Write([]byte("not a handshake"))
	require.NoError(t, err)

	var held net.Conn
	require.Eventually(t, func() bool {
		a.sudphSessions.mu.Lock()
		defer a.sudphSessions.mu.Unlock()
		for _, wp := range a.sudphSessions.m {
			if s := wp.Value(); s != nil {
				held = s
			}
		}
		return held != nil
	}, 5*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool {
		return a.sudphSessionsReport(0).Closed == 1
	}, 3*sudphHandshakeTimeout, 100*time.Millisecond)

	r := a.sudphSessionsReport(5)
	require.Equal(t, []string{fmt.Sprintf("%p", held)}, r.ClosedAddrs)
	require.Empty(t, a.sudphSessionsReport(0).ClosedAddrs)
	runtime.KeepAlive(held)
}
