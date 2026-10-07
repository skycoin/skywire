// Package dmsg pkg/dmsg/dmsg/client_dialfail_port_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// A dial that the destination refuses must close its stream and free the
// ephemeral port it reserved. Every refused dial used to keep both.
func TestDialStream_FailureFreesEphemeralPort(t *testing.T) {
	dc := disc.NewMock(0)
	srvPK, srvSK := GenKeyPair(t, "dialfail-srv")

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()

	srv := NewServer(srvPK, srvSK, dc, &ServerConfig{MaxSessions: 20, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("dialfail-srv"))
	entry := disc.NewServerEntry(srvPK, 0, addr, 20)
	require.NoError(t, entry.Sign(srvSK))
	require.NoError(t, dc.PostEntry(context.Background(), entry))
	go func() { _ = srv.Serve(lis, addr) }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })    //nolint:errcheck

	newClient := func(name string) (*Client, ClientSession) {
		pk, sk := GenKeyPair(t, name)
		c := NewClient(pk, sk, dc, &Config{MinSessions: 1})
		c.SetLogger(logging.MustGetLogger(name))
		t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
		ses, err := c.EnsureAndObtainSession(context.Background(), srvPK)
		require.NoError(t, err)
		return c, ses
	}
	a, aSes := newClient("dialfail-a")
	b, _ := newClient("dialfail-b")

	before := a.PorterCount()
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := aSes.DialStream(ctx, Addr{PK: b.LocalPK(), Port: 999})
		cancel()
		require.Error(t, err)
	}
	require.Equal(t, before, a.PorterCount(), "refused dials kept their ephemeral ports")
}
