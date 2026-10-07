// Package dmsgtest pkg/dmsg/dmsgtest/traffic_test.go
package dmsgtest

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// A client counting its traffic reports, per listening port, the streams it
// accepted and the bytes they carried each way. Ports it does not count, and
// streams it dials, stay out.
func TestCountTraffic(t *testing.T) {
	env := NewEnv(t, 30*time.Second)
	require.NoError(t, env.Startup(0, 1, 2, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	clients := env.AllClients()
	srv, cli := clients[0], clients[1]
	srv.CountTraffic()

	lis, err := srv.Listen(80)
	require.NoError(t, err)
	go func() {
		for {
			str, err := lis.AcceptStream()
			if err != nil {
				return
			}
			go func() {
				buf := make([]byte, 100)
				if _, err := io.ReadFull(str, buf); err == nil {
					_, _ = str.Write(append(buf, buf...)) //nolint:errcheck
				}
			}()
		}
	}()

	ctx := context.Background()
	str := dialStreamEventually(t, ctx, cli, dmsg.Addr{PK: srv.LocalPK(), Port: 80})
	_, err = str.Write(make([]byte, 100))
	require.NoError(t, err)
	_, err = io.ReadFull(str, make([]byte, 200))
	require.NoError(t, err)
	require.NoError(t, str.Close())

	require.Eventually(t, func() bool {
		tr := srv.Traffic()
		return len(tr) == 1 && tr[0].Port == 80 && tr[0].Streams == 1 && tr[0].In == 100 && tr[0].Out == 200
	}, 5*time.Second, 50*time.Millisecond, "got %+v", srv.Traffic())
	require.Empty(t, cli.Traffic(), "a client that does not count reports nothing")
}
