//go:build linux && !tinygo

// Package network pkg/transport/network/tcp_notsent_linux_test.go c2-net-transport
package network

import (
	"net"
	"testing"

	"golang.org/x/sys/unix"
)

func tcpOpt(t *testing.T, c *net.TCPConn, opt int) int {
	t.Helper()
	rc, err := c.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var v int
	var gerr error
	if err := rc.Control(func(fd uintptr) { v, gerr = unix.GetsockoptInt(int(fd), unix.IPPROTO_TCP, opt) }); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if gerr != nil {
		t.Fatal(gerr)
	}
	return v
}

// TestTuneTCPConnThroughDemux checks that a stcpr connection accepted on the
// shared transport port, which cmux wraps, gets the same tuning as a direct
// one: TCP_NODELAY and the TCP_NOTSENT_LOWAT limit.
func TestTuneTCPConnThroughDemux(t *testing.T) {
	master, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close() //nolint:errcheck
	d := newTCPDemux(master, false)

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := d.STCPR().Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- c
	}()
	dc, err := net.Dial("tcp", master.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer dc.Close() //nolint:errcheck
	if _, err := dc.Write([]byte("not http")); err != nil {
		t.Fatal(err)
	}
	c, ok := <-accepted
	if !ok {
		t.Fatal("demux accept failed")
	}
	defer c.Close() //nolint:errcheck

	SetTCPNotSentLowat(64 << 10)
	defer SetTCPNotSentLowat(DefaultTCPNotSentLowat)
	tuneTCPConn(c)

	tc, ok := unwrapTCPConn(c)
	if !ok {
		t.Fatalf("accepted %T has no TCP connection underneath", c)
	}
	if v := tcpOpt(t, tc, unix.TCP_NODELAY); v == 0 {
		t.Error("TCP_NODELAY is off on the accepted connection")
	}
	if v := tcpOpt(t, tc, unix.TCP_NOTSENT_LOWAT); v != 64<<10 {
		t.Errorf("TCP_NOTSENT_LOWAT = %d, want %d", v, 64<<10)
	}
}
