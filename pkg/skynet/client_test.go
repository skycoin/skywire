package skynet

import (
	"encoding/json"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestNewClientAndGetters(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	c := NewClient(testLog(), pk, 80, 8080, func() (net.Conn, error) { return nil, nil })
	require.Equal(t, pk, c.RemotePK())
	require.Equal(t, 80, c.RemotePort())
	require.Equal(t, 8080, c.LocalPort())
}

// skynetServerHandshake plays the server side of the skynet handshake on conn:
// send ready byte, read the port request, send the given reply, then (if
// echo) copy bytes back until the conn closes.
func skynetServerHandshake(conn net.Conn, reply serverReply, echo bool) {
	defer func() {
		if !echo {
			_ = conn.Close()
		}
	}()
	if _, err := conn.Write([]byte{1}); err != nil { // ready signal
		return
	}
	buf := make([]byte, 32*1024)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}
	var msg clientMsg
	_ = json.Unmarshal(buf[:n], &msg)
	data, _ := json.Marshal(reply)
	if _, err := conn.Write(data); err != nil {
		return
	}
	if echo {
		_, _ = io.Copy(conn, conn)
		_ = conn.Close()
	}
}

func TestClientConnect_Success(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	c := NewClient(testLog(), pk, 80, 0, nil)

	cliEnd, srvEnd := net.Pipe()
	go skynetServerHandshake(srvEnd, serverReply{}, false)

	require.NoError(t, cliEnd.SetDeadline(time.Now().Add(2*time.Second)))
	require.NoError(t, c.Connect(cliEnd))
}

func TestClientConnect_ServerError(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	c := NewClient(testLog(), pk, 80, 0, nil)

	cliEnd, srvEnd := net.Pipe()
	errStr := "port not exposed"
	go skynetServerHandshake(srvEnd, serverReply{Error: &errStr}, false)

	require.NoError(t, cliEnd.SetDeadline(time.Now().Add(2*time.Second)))
	err := c.Connect(cliEnd)
	require.Error(t, err)
	require.Contains(t, err.Error(), "server error: port not exposed")
}

func TestClientConnect_ReadyReadFails(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	c := NewClient(testLog(), pk, 80, 0, nil)

	cliEnd, srvEnd := net.Pipe()
	_ = srvEnd.Close() // no ready signal → read fails
	err := c.Connect(cliEnd)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to read ready signal")
}

func TestClientConnect_BadReplyJSON(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	c := NewClient(testLog(), pk, 80, 0, nil)

	cliEnd, srvEnd := net.Pipe()
	go func() {
		_, _ = srvEnd.Write([]byte{1}) // ready
		buf := make([]byte, 1024)
		_, _ = srvEnd.Read(buf)             // port request
		_, _ = srvEnd.Write([]byte("{bad")) // malformed reply
		_ = srvEnd.Close()
	}()

	require.NoError(t, cliEnd.SetDeadline(time.Now().Add(2*time.Second)))
	err := c.Connect(cliEnd)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to parse response")
}

func TestClientServe_ListenError(t *testing.T) {
	// Occupy a port, then point the client's local listener at it.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer occupied.Close() //nolint:errcheck
	port := occupied.Addr().(*net.TCPAddr).Port

	pk, _ := cipher.GenerateKeyPair()
	c := NewClient(testLog(), pk, 80, port, func() (net.Conn, error) { return nil, nil })
	err = c.Serve()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to listen")
}

func TestClientServe_EndToEnd(t *testing.T) {
	// Fake remote server: a TCP listener that runs the handshake then echoes.
	remoteLis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer remoteLis.Close() //nolint:errcheck
	go func() {
		for {
			conn, aerr := remoteLis.Accept()
			if aerr != nil {
				return
			}
			go skynetServerHandshake(conn, serverReply{}, true)
		}
	}()

	dialRemote := func() (net.Conn, error) { return net.Dial("tcp", remoteLis.Addr().String()) }

	localPort := freePort(t)
	pk, _ := cipher.GenerateKeyPair()
	c := NewClient(testLog(), pk, 80, localPort, dialRemote)

	serveErr := make(chan error, 1)
	go func() { serveErr <- c.Serve() }()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort))
	waitForListen(t, addr)

	// Connect to the local listener and round-trip bytes through the tunnel.
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	payload := []byte("hello-skynet")
	require.NoError(t, conn.SetDeadline(time.Now().Add(3*time.Second)))
	_, err = conn.Write(payload)
	require.NoError(t, err)

	got := readN(t, conn, len(payload))
	require.Equal(t, payload, got)
	_ = conn.Close()

	require.NoError(t, c.Close())
	require.NoError(t, c.Close()) // idempotent
	select {
	case err := <-serveErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after Close")
	}
}

func TestClientHandleLocalConn_DialRemoteFails(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	c := NewClient(testLog(), pk, 80, 0, func() (net.Conn, error) {
		return nil, io.ErrClosedPipe // dial always fails
	})

	cli, srv := net.Pipe()
	done := make(chan struct{})
	go func() { c.handleLocalConn(srv); close(done) }()

	// handleLocalConn must close the local conn when the remote dial fails.
	require.NoError(t, cli.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, err := cli.Read(make([]byte, 1))
	require.Error(t, err) // EOF: server end closed
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleLocalConn did not return")
	}
}

func TestHalfCloseWrite_FullCloseFallback(t *testing.T) {
	// net.Pipe conns don't implement CloseWrite → halfCloseWrite falls back
	// to a full Close.
	a, b := net.Pipe()
	defer b.Close() //nolint:errcheck
	halfCloseWrite(testLog(), a, "test")
	// After a full Close, a write must fail.
	_, err := a.Write([]byte("x"))
	require.Error(t, err)
}

func waitForListen(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listener at %s never came up", addr)
}
