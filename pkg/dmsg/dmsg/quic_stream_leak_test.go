//go:build !tinygo

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

// TestQUICSession_ClosedStreamsFreeTheirSlots dials and closes many more
// streams than the QUIC stream limit, one after another. A closed stream must
// give its slot back, or the session stops opening streams once the limit is
// used up while the streams already open keep working.
func TestQUICSession_ClosedStreamsFreeTheirSlots(t *testing.T) {
	prevMax, prevOpen := quicMaxIncomingStreams, quicStreamOpenTimeout
	quicMaxIncomingStreams, quicStreamOpenTimeout = 20, 3*time.Second
	t.Cleanup(func() { quicMaxIncomingStreams, quicStreamOpenTimeout = prevMax, prevOpen })

	dc := disc.NewMock(0)
	pkSrv, skSrv := GenKeyPair(t, "server")
	srv := NewServer(pkSrv, skSrv, dc, &ServerConfig{MaxSessions: 10, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("quic_server"))
	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	udpAddr := udpConn.LocalAddr().String()
	go func() { _ = srv.ServeQUIC(udpConn, udpAddr) }() //nolint:errcheck
	srvEntry := disc.NewServerEntry(pkSrv, 0, "", 10)
	srvEntry.Server.Address = ""
	srvEntry.Server.AddressUDP = udpAddr
	srvEntry.Protocol = "quic"
	require.NoError(t, srvEntry.Sign(skSrv))
	require.NoError(t, dc.PostEntry(context.Background(), srvEntry))

	newClient := func(name string) (*Client, cipher.PubKey) {
		pk, sk := GenKeyPair(t, name)
		c := DefaultConfig()
		c.Protocol = "quic"
		cl := NewClient(pk, sk, dc, c)
		cl.SetLogger(logging.MustGetLogger(name))
		go cl.Serve(context.Background())
		return cl, pk
	}
	clientA, _ := newClient("client A")
	clientB, pkB := newClient("client B")
	defer func() {
		_ = clientA.Close() //nolint:errcheck
		_ = clientB.Close() //nolint:errcheck
		_ = srv.Close()     //nolint:errcheck
	}()
	require.Eventually(t, func() bool {
		return clientA.SessionCount() > 0 && clientB.SessionCount() > 0
	}, 10*time.Second, 200*time.Millisecond)

	lis, err := clientB.Listen(8080)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			go func() {
				b := make([]byte, 1)
				if _, err := io.ReadFull(c, b); err == nil {
					_, _ = c.Write(b) //nolint:errcheck
				}
				_ = c.Close() //nolint:errcheck
			}()
		}
	}()

	const n = 200
	for i := 0; i < n; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*quicStreamOpenTimeout)
		conn, err := clientA.DialStream(ctx, Addr{PK: pkB, Port: 8080})
		cancel()
		require.NoError(t, err, "stream %d of %d", i+1, n)
		_, err = conn.Write([]byte{1})
		require.NoError(t, err)
		b := make([]byte, 1)
		_, err = io.ReadFull(conn, b)
		require.NoError(t, err, "echo on stream %d", i+1)
		require.NoError(t, conn.Close())
	}

	// A dial to a port nobody listens on is refused; that must free its slot too.
	const refused = 25 // past the limit of 20
	for i := 0; i < refused; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*quicStreamOpenTimeout)
		_, err := clientA.DialStream(ctx, Addr{PK: pkB, Port: 9999})
		cancel()
		require.Error(t, err)
		clientA.dialFailClear(pkB) // the backoff would skip the next dial without opening a stream
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*quicStreamOpenTimeout)
	defer cancel()
	conn, err := clientA.DialStream(ctx, Addr{PK: pkB, Port: 8080})
	require.NoError(t, err, "a dial after %d refused dials", refused)
	_ = conn.Close() //nolint:errcheck
}
