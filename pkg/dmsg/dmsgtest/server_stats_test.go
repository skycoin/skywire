// Package dmsgtest pkg/dmsg/dmsgtest/server_stats_test.go
package dmsgtest

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// A server counts the sessions it holds, and the streams it relays between
// them with the bytes they carry each way.
func TestServerStats(t *testing.T) {
	env := NewEnv(t, 30*time.Second)
	require.NoError(t, env.Startup(0, 1, 2, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	srv := env.AllServers()[0]
	clients := env.AllClients()
	dst, src := clients[0], clients[1]

	lis, err := dst.Listen(80)
	require.NoError(t, err)
	go func() {
		str, err := lis.AcceptStream()
		if err != nil {
			return
		}
		buf := make([]byte, 100)
		if _, err := io.ReadFull(str, buf); err == nil {
			_, _ = str.Write(make([]byte, 300)) //nolint:errcheck
		}
		_ = str.Close() //nolint:errcheck
	}()

	str := dialStreamEventually(t, context.Background(), src, dmsg.Addr{PK: dst.LocalPK(), Port: 80})
	_, err = str.Write(make([]byte, 100))
	require.NoError(t, err)
	_, err = io.ReadFull(str, make([]byte, 300))
	require.NoError(t, err)
	require.Equal(t, int64(1), srv.Stats().ActiveStreams)
	require.NoError(t, str.Close())

	require.Eventually(t, func() bool {
		st := srv.Stats()
		return st.ActiveStreams == 0 && st.StreamsRelayed == 1 && st.BytesUp >= 100 && st.BytesDown >= 300
	}, 10*time.Second, 50*time.Millisecond, "got %+v", srv.Stats())
	st := srv.Stats()
	require.Equal(t, 2, st.ClientSessions)
	require.Zero(t, st.PeerSessions)
}
