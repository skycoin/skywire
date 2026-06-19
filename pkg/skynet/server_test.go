package skynet

import (
	"encoding/json"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
)

func testLog() logrus.FieldLogger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l
}

// pkConn wraps a net.Conn so its addresses report an appnet.Addr, letting
// appnet.WrapConn succeed and yield a *appnet.WrappedConn carrying a chosen
// remote public key — exactly what Server.handleConn type-asserts.
type pkConn struct {
	net.Conn
	addr appnet.Addr
}

func (c pkConn) LocalAddr() net.Addr  { return c.addr }
func (c pkConn) RemoteAddr() net.Addr { return c.addr }

func wrappedPipe(t *testing.T, pk cipher.PubKey) (client net.Conn, wrapped net.Conn) {
	t.Helper()
	cliEnd, srvEnd := net.Pipe()
	w, err := appnet.WrapConn(pkConn{Conn: srvEnd, addr: appnet.Addr{Net: appnet.TypeSkynet, PubKey: pk}})
	require.NoError(t, err)
	return cliEnd, w
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

// --- map / config accessors --------------------------------------------

func TestServerPorts(t *testing.T) {
	s := NewServer(testLog())
	require.Empty(t, s.Ports())

	s.AddPort(8080)
	s.AddPort(9090)
	require.ElementsMatch(t, []int{8080, 9090}, s.Ports())

	s.RemovePort(8080)
	require.ElementsMatch(t, []int{9090}, s.Ports())
}

func TestServerWhitelist(t *testing.T) {
	s := NewServer(testLog())
	require.Empty(t, s.Whitelist())
	require.False(t, s.useWL)

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	s.AddToWhitelist(pk1)
	s.AddToWhitelist(pk2)
	require.True(t, s.useWL)
	require.Len(t, s.Whitelist(), 2)

	s.RemoveFromWhitelist(pk1)
	require.Len(t, s.Whitelist(), 1)
	require.True(t, s.useWL) // still one entry

	s.RemoveFromWhitelist(pk2)
	require.Empty(t, s.Whitelist())
	require.False(t, s.useWL) // empties → whitelist disabled
}

// --- sendError ----------------------------------------------------------

func TestSendError(t *testing.T) {
	s := NewServer(testLog())

	read := func(send error) serverReply {
		cli, srv := net.Pipe()
		defer cli.Close() //nolint:errcheck
		go func() {
			s.sendError(srv, send)
			_ = srv.Close()
		}()
		var reply serverReply
		require.NoError(t, cli.SetReadDeadline(time.Now().Add(2*time.Second)))
		require.NoError(t, json.NewDecoder(cli).Decode(&reply))
		return reply
	}

	require.Nil(t, read(nil).Error) // success reply: no error field

	r := read(io.ErrUnexpectedEOF)
	require.NotNil(t, r.Error)
	require.Equal(t, io.ErrUnexpectedEOF.Error(), *r.Error)
}

// --- handleConn ---------------------------------------------------------

func TestHandleConn_NotWrappedConn(t *testing.T) {
	s := NewServer(testLog())
	_, srv := net.Pipe()
	// A plain pipe conn is not a *appnet.WrappedConn → handleConn returns.
	require.NotPanics(t, func() { s.handleConn(srv) })
}

func TestHandleConn_WhitelistReject(t *testing.T) {
	s := NewServer(testLog())
	allowed, _ := cipher.GenerateKeyPair()
	s.AddToWhitelist(allowed) // turns on useWL; our caller PK differs

	caller, _ := cipher.GenerateKeyPair()
	cli, wrapped := wrappedPipe(t, caller)
	defer cli.Close() //nolint:errcheck

	go s.handleConn(wrapped)

	var reply serverReply
	require.NoError(t, cli.SetReadDeadline(time.Now().Add(2*time.Second)))
	require.NoError(t, json.NewDecoder(cli).Decode(&reply))
	require.NotNil(t, reply.Error)
	require.Contains(t, *reply.Error, "not authorized")
}

func TestHandleConn_PortNotExposed(t *testing.T) {
	s := NewServer(testLog())
	caller, _ := cipher.GenerateKeyPair()
	cli, wrapped := wrappedPipe(t, caller)
	defer cli.Close() //nolint:errcheck

	go s.handleConn(wrapped)

	// Send a request for a port that was never AddPort'd.
	require.NoError(t, cli.SetWriteDeadline(time.Now().Add(2*time.Second)))
	_, err := cli.Write(mustJSON(t, clientMsg{Port: 4321}))
	require.NoError(t, err)

	var reply serverReply
	require.NoError(t, cli.SetReadDeadline(time.Now().Add(2*time.Second)))
	require.NoError(t, json.NewDecoder(cli).Decode(&reply))
	require.NotNil(t, reply.Error)
	require.Contains(t, *reply.Error, "not exposed")
}

func TestHandleConn_BadJSON(t *testing.T) {
	s := NewServer(testLog())
	caller, _ := cipher.GenerateKeyPair()
	cli, wrapped := wrappedPipe(t, caller)
	defer cli.Close() //nolint:errcheck

	go s.handleConn(wrapped)

	require.NoError(t, cli.SetWriteDeadline(time.Now().Add(2*time.Second)))
	_, err := cli.Write([]byte("{not valid json"))
	require.NoError(t, err)

	var reply serverReply
	require.NoError(t, cli.SetReadDeadline(time.Now().Add(2*time.Second)))
	require.NoError(t, json.NewDecoder(cli).Decode(&reply))
	require.NotNil(t, reply.Error) // unmarshal error surfaced to client
}

func TestHandleConn_ReadError(t *testing.T) {
	s := NewServer(testLog())
	caller, _ := cipher.GenerateKeyPair()
	cli, wrapped := wrappedPipe(t, caller)
	// Closing the client end immediately makes the server's Read fail.
	require.NoError(t, cli.Close())
	require.NotPanics(t, func() { s.handleConn(wrapped) })
}

func TestHandleConn_SuccessAndForward(t *testing.T) {
	s := NewServer(testLog())

	// Local echo server that handleConn will forward to.
	echoLis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer echoLis.Close() //nolint:errcheck
	go func() {
		c, aerr := echoLis.Accept()
		if aerr != nil {
			return
		}
		_, _ = io.Copy(c, c) // echo
		_ = c.Close()
	}()
	echoPort := echoLis.Addr().(*net.TCPAddr).Port
	s.AddPort(echoPort)

	caller, _ := cipher.GenerateKeyPair()
	cli, wrapped := wrappedPipe(t, caller)
	defer cli.Close() //nolint:errcheck

	done := make(chan struct{})
	go func() { s.handleConn(wrapped); close(done) }()

	// Request the exposed port.
	_, err = cli.Write(mustJSON(t, clientMsg{Port: echoPort}))
	require.NoError(t, err)

	// First read is the success reply (no error).
	var reply serverReply
	dec := json.NewDecoder(cli)
	require.NoError(t, cli.SetReadDeadline(time.Now().Add(2*time.Second)))
	require.NoError(t, dec.Decode(&reply))
	require.Nil(t, reply.Error)

	// Now the conn is a raw byte tunnel to the echo server.
	payload := []byte("ping-through-tunnel")
	_, err = cli.Write(payload)
	require.NoError(t, err)

	require.NoError(t, cli.SetReadDeadline(time.Now().Add(2*time.Second)))
	// dec may have buffered bytes from the reply read; drain those first.
	got := readN(t, io.MultiReader(dec.Buffered(), cli), len(payload))
	require.Equal(t, payload, got)

	_ = cli.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handleConn did not return after client close")
	}
}

// --- forwardRawTCP dial failure ----------------------------------------

func TestForwardRawTCP_DialFail(t *testing.T) {
	s := NewServer(testLog())
	_, srv := net.Pipe()
	defer srv.Close() //nolint:errcheck
	// Nothing listening on this port → net.Dial fails fast on loopback.
	require.NotPanics(t, func() {
		s.forwardRawTCP(srv, net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t))))
	})
}

// --- Serve + Close ------------------------------------------------------

func TestServerServeAndClose(t *testing.T) {
	s := NewServer(testLog())
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Serve(lis) }()

	// A plain (non-wrapped) connection is accepted then dropped by handleConn.
	conn, err := net.Dial("tcp", lis.Addr().String())
	require.NoError(t, err)
	_ = conn.Close()

	time.Sleep(100 * time.Millisecond)
	require.NoError(t, s.Close())
	require.NoError(t, s.Close()) // idempotent

	select {
	case err := <-serveErr:
		require.NoError(t, err) // closed cleanly
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after Close")
	}
}

// --- helpers ------------------------------------------------------------

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func readN(t *testing.T, r io.Reader, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	require.NoError(t, err)
	return buf
}
