// Package dmsg pkg/dmsg/dmsg/session_handshake_tail_test.go c2-dmsg-core
package dmsg

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/logging"
)

// holdLastConn passes the first write straight through and holds every later
// write until Flush, so the initiator's final handshake message and the
// session bytes behind it leave in ONE TCP write — the coalescing that a
// peering server produces in the wild when it announces itself right after
// the handshake.
type holdLastConn struct {
	net.Conn
	mu     sync.Mutex
	writes int
	held   bytes.Buffer
}

func (c *holdLastConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes++
	if c.writes == 1 {
		return c.Conn.Write(p)
	}
	return c.held.Write(p)
}

func (c *holdLastConn) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.Conn.Write(c.held.Bytes())
	c.held.Reset()
	return err
}

// The responder must keep session bytes that arrive behind the initiator's
// last handshake message instead of failing with "extra bytes received
// during session handshake" (dmsg error 203) and dropping the connection.
func TestServerSession_KeepsBytesBehindHandshake(t *testing.T) {
	sPK, sSK := GenKeyPair(t, "server")
	cPK, cSK := GenKeyPair(t, "client")
	ent := &EntityCommon{}
	ent.init(sPK, sSK, disc.NewMock(0), logging.MustGetLogger("hs-tail"), 0)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close() //nolint:errcheck

	const payload = "session-bytes-behind-msg3"
	got := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errs <- err
			return
		}
		sc := new(SessionCommon)
		if err := sc.initServer(ent, conn); err != nil {
			errs <- err
			return
		}
		buf := make([]byte, len(payload))
		_ = sc.GetConn().SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
		if _, err := io.ReadFull(sc.GetConn(), buf); err != nil {
			errs <- err
			return
		}
		got <- string(buf)
	}()

	raw, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer raw.Close() //nolint:errcheck
	conn := &holdLastConn{Conn: raw}
	ns, err := noise.New(noise.HandshakeXK, noise.Config{LocalPK: cPK, LocalSK: cSK, RemotePK: sPK, Initiator: true})
	require.NoError(t, err)
	rw := noise.NewReadWriter(conn, ns)
	require.NoError(t, rw.Handshake(HandshakeTimeout)) // msg3 is now held
	_, err = conn.Write([]byte(payload))               // rides behind msg3
	require.NoError(t, err)
	require.NoError(t, conn.Flush())

	select {
	case s := <-got:
		require.Equal(t, payload, s)
	case err := <-errs:
		t.Fatalf("responder failed: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("responder did not deliver the trailing bytes")
	}
}
