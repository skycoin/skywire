package api

import (
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
		return a.sudphSessionsReport().Accepted == 1
	}, 5*time.Second, 50*time.Millisecond)
	require.NoError(t, c.Close())

	require.Eventually(t, func() bool {
		runtime.GC()
		r := a.sudphSessionsReport()
		return r.Closed == 1 && r.InMemory == 0
	}, 3*sudphHandshakeTimeout, 200*time.Millisecond)
}
