// Package dmsg pkg/dmsg/dmsg/server_mux_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// stuckEntries is a discovery whose lookups of any key but the server's
// never return — as a lookup of a client with no entry (a browser, a direct
// client) can take as long as the discovery likes.
type stuckEntries struct {
	disc.APIClient
	server cipher.PubKey
}

func (d stuckEntries) Entry(ctx context.Context, pk cipher.PubKey) (*disc.Entry, error) {
	if pk == d.server {
		return d.APIClient.Entry(ctx, pk)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// A session's streams must not wait on discovery: the server serves every
// session as yamux without looking its client up.
func TestServer_SessionDoesNotWaitOnDiscovery(t *testing.T) {
	// Run apart, so a session stuck on discovery fails the test rather than
	// hanging it (the stuck session also holds up Server.Close).
	done := make(chan error, 1)
	go func() { done <- dialThroughStuckDiscovery() }()
	select {
	case err := <-done:
		require.NoError(t, err, "a stream through the server waited on a discovery lookup of its client")
	case <-time.After(20 * time.Second):
		t.Fatal("no stream through the server in 20s: its session is waiting on a discovery lookup of its client")
	}
}

func dialThroughStuckDiscovery() error {
	dc := disc.NewMock(0)
	pk, sk := cipher.GenerateKeyPair()
	lis, err := nettest.NewLocalListener("tcp")
	if err != nil {
		return err
	}
	srv := NewServer(pk, sk, stuckEntries{dc, pk}, &ServerConfig{MaxSessions: 10, UpdateInterval: DefaultUpdateInterval}, nil)
	go srv.Serve(lis, "") //nolint:errcheck
	defer srv.Close()     //nolint:errcheck
	<-srv.Ready()

	newClient := func() *Client {
		cpk, csk := cipher.GenerateKeyPair()
		c := NewClient(cpk, csk, dc, &Config{MinSessions: 1})
		go c.Serve(context.Background())
		<-c.Ready()
		return c
	}
	a, b := newClient(), newClient()
	defer a.Close() //nolint:errcheck
	defer b.Close() //nolint:errcheck

	l, err := a.Listen(9)
	if err != nil {
		return err
	}
	defer l.Close() //nolint:errcheck
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close() //nolint:errcheck,gosec
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := b.DialStream(ctx, Addr{PK: a.LocalPK(), Port: 9})
	if err != nil {
		return err
	}
	return s.Close()
}
