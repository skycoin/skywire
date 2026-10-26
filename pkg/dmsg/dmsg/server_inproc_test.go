package dmsg

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// Clients in the same process as a server that serves in-process hold their
// sessions with it over a pipe, and streams relay between them as usual. Once
// the server closes, clients dial it over the network again.
func TestServeInProcess(t *testing.T) {
	dc := disc.NewMock(0)
	srvPK, srvSK := GenKeyPair(t, "inproc-server")
	srv := NewServer(srvPK, srvSK, dc, &ServerConfig{MaxSessions: 10}, nil)
	srv.SetLogger(logging.MustGetLogger("inproc_server"))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	entry := disc.NewServerEntry(srvPK, 0, lis.Addr().String(), 10)
	require.NoError(t, entry.Sign(srvSK))
	require.NoError(t, dc.PostEntry(context.Background(), entry))
	go func() { _ = srv.Serve(lis, lis.Addr().String()) }() //nolint:errcheck
	srv.ServeInProcess()
	require.Same(t, srv, inProcessServer(srvPK))

	newClient := func(seed string) *Client {
		pk, sk := GenKeyPair(t, seed)
		c := NewClient(pk, sk, dc, &Config{MinSessions: 1})
		c.SetLogger(logging.MustGetLogger(seed))
		go c.Serve(context.Background())
		t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
		return c
	}
	session := func(c *Client) ClientSession {
		var ses ClientSession
		require.Eventually(t, func() bool {
			var ok bool
			ses, ok = c.Session(srvPK)
			return ok
		}, 15*time.Second, 50*time.Millisecond)
		return ses
	}
	a, b := newClient("inproc-a"), newClient("inproc-b")
	require.Equal(t, CarrierInProcess, session(a).Carrier())
	require.Equal(t, CarrierInProcess, session(b).Carrier())

	const port = 80
	lisA, err := a.Listen(port)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lisA.Close() }) //nolint:errcheck
	go func() {
		s, aerr := lisA.AcceptStream()
		if aerr != nil {
			return
		}
		defer s.Close()      //nolint:errcheck
		_, _ = io.Copy(s, s) //nolint:errcheck
	}()

	var str *Stream
	require.Eventually(t, func() bool {
		var derr error
		str, derr = b.DialStream(context.Background(), Addr{PK: a.LocalPK(), Port: port})
		return derr == nil
	}, 15*time.Second, 100*time.Millisecond)
	defer str.Close() //nolint:errcheck
	msg := []byte("over the pipe")
	_, err = str.Write(msg)
	require.NoError(t, err)
	got := make([]byte, len(msg))
	require.NoError(t, str.SetReadDeadline(time.Now().Add(10*time.Second)))
	_, err = io.ReadFull(str, got)
	require.NoError(t, err)
	require.Equal(t, msg, got)

	require.NoError(t, srv.Close())
	require.Eventually(t, func() bool { return inProcessServer(srvPK) == nil }, 5*time.Second, 20*time.Millisecond)
	var pk cipher.PubKey
	require.Nil(t, inProcessServer(pk))
}
