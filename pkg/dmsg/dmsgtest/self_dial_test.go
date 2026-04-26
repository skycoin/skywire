package dmsgtest

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// TestSelfDial verifies that a dmsg client can Dial its own PK and send
// data through the resulting stream. Historically this has been broken
// because the server-side bridge reuses the same yamux session for both
// halves of the bridge — this test pins the expected behavior.
//
// Uses 3 servers / MinSessions=3 to exercise the multi-delegated-server
// case that production visors see, not just the trivial 1-server setup.
func TestSelfDial(t *testing.T) {
	const timeout = 30 * time.Second

	env := NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 3, 1, &dmsg.Config{MinSessions: 3}))
	t.Cleanup(env.Shutdown)

	clients := env.AllClients()
	require.Len(t, clients, 1)
	client := clients[0]

	// Use port 1 — the same port skychat listens on — so we're not
	// accidentally hiding a port-specific bug.
	const port = uint16(1)

	lis, err := client.Listen(port)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lis.Close() }) //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Dialing leg: client dials its own PK.
	dialDone := make(chan struct{})
	var dialStream *dmsg.Stream
	var dialErr error
	go func() {
		defer close(dialDone)
		dialStream, dialErr = client.DialStream(ctx, dmsg.Addr{PK: client.LocalPK(), Port: port})
	}()

	// Accepting leg: listener accepts the loopback stream.
	acceptStream, err := lis.AcceptStream()
	require.NoError(t, err, "AcceptStream on self-listen failed")
	t.Cleanup(func() { _ = acceptStream.Close() }) //nolint:errcheck

	<-dialDone
	require.NoError(t, dialErr, "DialStream to self failed")
	t.Cleanup(func() { _ = dialStream.Close() }) //nolint:errcheck

	// Bidirectional payload test.
	payloads := []int{64, 4 * 1024}
	for _, size := range payloads {
		// dialer -> acceptor
		dataD2A := cipher.RandByte(size)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, werr := dialStream.Write(dataD2A)
			assert.NoError(t, werr)
		}()
		recvD2A := make([]byte, size)
		_, err = io.ReadFull(acceptStream, recvD2A)
		require.NoError(t, err)
		wg.Wait()
		require.True(t, bytes.Equal(dataD2A, recvD2A), "dial→accept data mismatch")

		// acceptor -> dialer
		dataA2D := cipher.RandByte(size)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, werr := acceptStream.Write(dataA2D)
			assert.NoError(t, werr)
		}()
		recvA2D := make([]byte, size)
		_, err = io.ReadFull(dialStream, recvA2D)
		require.NoError(t, err)
		wg.Wait()
		require.True(t, bytes.Equal(dataA2D, recvA2D), "accept→dial data mismatch")
	}
}
