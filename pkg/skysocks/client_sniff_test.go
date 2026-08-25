// Package skysocks SOCKS5 sniff / status-snapshot coverage tests.
package skysocks

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// socks5ConnectATYP performs a greeting + CONNECT using an explicit ATYP/addr so
// the IPv4 (0x01) and IPv6 (0x04) request-parsing branches of sniffSOCKS5Status
// are exercised (the existing tests only use the domain 0x03 branch).
func socks5ConnectATYP(t *testing.T, addr string, atyp byte, rawAddr []byte, port uint16) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	_, err = conn.Write([]byte{0x05, 0x01, 0x00})
	require.NoError(t, err)
	method := make([]byte, 2)
	_, err = io.ReadFull(conn, method)
	require.NoError(t, err)
	require.Equal(t, []byte{0x05, 0x00}, method)

	req := []byte{0x05, 0x01, 0x00, atyp}
	req = append(req, rawAddr...)
	req = append(req, byte(port>>8), byte(port)) //nolint:gosec
	_, err = conn.Write(req)
	require.NoError(t, err)
	reply := make([]byte, 10)
	_, err = io.ReadFull(conn, reply)
	require.NoError(t, err)
	return conn
}

func startClientListener(t *testing.T, c *Client) string {
	t.Helper()
	addr := freeAddr(t)
	go func() { _ = c.ListenAndServe(addr) }() //nolint:errcheck
	waitDial(t, addr)
	return addr
}

// A CONNECT with an IPv4 ATYP is forwarded to the exit unchanged.
func TestSniff_IPv4Forwarded(t *testing.T) {
	c, exit := newTestClient(t)
	defer c.Close() //nolint:errcheck
	addr := startClientListener(t, c)

	conn := socks5ConnectATYP(t, addr, 0x01, []byte{93, 184, 216, 34}, 80)
	defer conn.Close() //nolint:errcheck

	select {
	case h := <-exit.hostC:
		require.Equal(t, "93.184.216.34", h)
	case <-time.After(2 * time.Second):
		t.Fatal("exit never received the IPv4 CONNECT")
	}
}

// A CONNECT with an IPv6 ATYP is forwarded to the exit unchanged.
func TestSniff_IPv6Forwarded(t *testing.T) {
	c, exit := newTestClient(t)
	defer c.Close() //nolint:errcheck
	addr := startClientListener(t, c)

	v6 := net.ParseIP("2001:4860:4860::8888").To16()
	require.NotNil(t, v6)
	conn := socks5ConnectATYP(t, addr, 0x04, v6, 443)
	defer conn.Close() //nolint:errcheck

	select {
	case h := <-exit.hostC:
		require.Equal(t, "2001:4860:4860::8888", h)
	case <-time.After(2 * time.Second):
		t.Fatal("exit never received the IPv6 CONNECT")
	}
}

// runSniff drives c.sniffSOCKS5Status against two in-memory pipe pairs and hands
// the test the browser and exit ends so it can feed a scripted negotiation and
// assert the proceed result. This unit-tests the sniff's branch logic directly,
// without the yamux/ListenAndServe machinery.
func runSniff(t *testing.T, c *Client) (browser, exit net.Conn, result <-chan bool) {
	t.Helper()
	connSniff, browserEnd := net.Pipe()
	streamSniff, exitEnd := net.Pipe()
	res := make(chan bool, 1)
	go func() {
		proceed, _ := c.sniffSOCKS5Status(connSniff, streamSniff)
		res <- proceed
		_ = connSniff.Close()   //nolint:errcheck
		_ = streamSniff.Close() //nolint:errcheck
	}()
	return browserEnd, exitEnd, res
}

// When the exit selects a method other than no-auth, the client hands off
// transparently (proceed=true) without buffering/parsing the CONNECT request.
func TestSniff_NonNoAuthHandoff(t *testing.T) {
	c, _ := newTestClient(t)
	defer c.Close() //nolint:errcheck
	browser, exit, res := runSniff(t, c)
	defer browser.Close() //nolint:errcheck
	defer exit.Close()    //nolint:errcheck

	// Browser greeting → forwarded to exit verbatim.
	_, err := browser.Write([]byte{0x05, 0x01, 0x00})
	require.NoError(t, err)
	got := make([]byte, 3)
	_, err = io.ReadFull(exit, got)
	require.NoError(t, err)
	require.Equal(t, []byte{0x05, 0x01, 0x00}, got)

	// Exit picks user/pass auth (05 02) → forwarded to browser; sniff hands off.
	_, err = exit.Write([]byte{0x05, 0x02})
	require.NoError(t, err)
	reply := make([]byte, 2)
	_, err = io.ReadFull(browser, reply)
	require.NoError(t, err)
	require.Equal(t, []byte{0x05, 0x02}, reply)

	select {
	case proceed := <-res:
		require.True(t, proceed, "non-no-auth reply must proceed to a transparent splice")
	case <-time.After(2 * time.Second):
		t.Fatal("sniff did not return after non-no-auth handoff")
	}
}

// An unknown CONNECT address type is forwarded to the exit as-is and the sniff
// hands off (proceed=true) rather than parsing it.
func TestSniff_UnknownATYPHandoff(t *testing.T) {
	c, _ := newTestClient(t)
	defer c.Close() //nolint:errcheck
	browser, exit, res := runSniff(t, c)
	defer browser.Close() //nolint:errcheck
	defer exit.Close()    //nolint:errcheck

	_, err := browser.Write([]byte{0x05, 0x01, 0x00}) // greeting
	require.NoError(t, err)
	_, _ = io.ReadFull(exit, make([]byte, 3)) //nolint:errcheck
	_, err = exit.Write([]byte{0x05, 0x00})   // no-auth selected
	require.NoError(t, err)
	_, _ = io.ReadFull(browser, make([]byte, 2)) //nolint:errcheck

	// CONNECT with an unknown ATYP (0x09).
	_, err = browser.Write([]byte{0x05, 0x01, 0x00, 0x09})
	require.NoError(t, err)
	fwd := make([]byte, 4)
	_, err = io.ReadFull(exit, fwd)
	require.NoError(t, err)
	require.Equal(t, byte(0x09), fwd[3])

	select {
	case proceed := <-res:
		require.True(t, proceed, "unknown ATYP must be forwarded and proceed")
	case <-time.After(2 * time.Second):
		t.Fatal("sniff did not return for unknown ATYP")
	}
}

// A greeting that is not SOCKS5 (bad version byte) is rejected (proceed=false).
func TestSniff_BadVersionRejected(t *testing.T) {
	c, _ := newTestClient(t)
	defer c.Close() //nolint:errcheck
	browser, exit, res := runSniff(t, c)
	defer browser.Close() //nolint:errcheck
	defer exit.Close()    //nolint:errcheck

	_, err := browser.Write([]byte{0x04, 0x01}) // SOCKS4 version → not 0x05
	require.NoError(t, err)
	select {
	case proceed := <-res:
		require.False(t, proceed, "a non-SOCKS5 greeting must not proceed")
	case <-time.After(2 * time.Second):
		t.Fatal("sniff did not reject a bad version")
	}
}

// statusSnapshot reflects a live session (running, stream count) and a torn-down
// one (not running).
func TestStatusSnapshot(t *testing.T) {
	c, exit := newTestClient(t)
	_ = exit

	snap := c.statusSnapshot()
	require.True(t, snap.Running, "fresh session should report running")
	require.Contains(t, snap.Note, "session to the exit is up")

	require.NoError(t, c.Close())
	snap = c.statusSnapshot()
	require.False(t, snap.Running, "closed session should report not running")
	require.Contains(t, snap.Note, "no active session")
}
