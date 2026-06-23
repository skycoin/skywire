//go:build !tinygo

package network

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/AudriusButkevicius/pfilter"
	kcp "github.com/xtaci/kcp-go"
)

// TestUDPDemux_KCPRealTraffic validates the demux against REAL sudph traffic: a
// kcp-go listener runs over a pfilter wrapping the demux's sudph virtual conn (the
// exact stack sudph.listen() builds), a kcp-go dialer connects to the shared
// socket's address, and the demux must classify+route the actual KCP packets. The
// risk this de-risks is pfilter operating over a demuxConn rather than a real UDP
// socket. Full session + echo round-trips.
func TestUDPDemux_KCPRealTraffic(t *testing.T) {
	master, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("master socket: %v", err)
	}
	d := newUDPDemux(master)
	defer d.Close() //nolint:errcheck

	// Server stack, mirroring sudph.listen(): pfilter over the sudph demux conn,
	// a catch-all conn, kcp.ServeConn over it.
	filter := pfilter.NewPacketFilter(d.Conn(protoSUDPH))
	srvConn := filter.NewConn(100, nil)
	filter.Start()
	lis, err := kcp.ServeConn(nil, 0, 0, srvConn)
	if err != nil {
		t.Fatalf("kcp.ServeConn over demux+pfilter: %v", err)
	}
	defer lis.Close() //nolint:errcheck

	srvErr := make(chan error, 1)
	go func() {
		sess, err := lis.AcceptKCP()
		if err != nil {
			srvErr <- err
			return
		}
		_ = sess.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
		buf := make([]byte, 4)
		if _, err := io.ReadFull(sess, buf); err != nil {
			srvErr <- err
			return
		}
		_, err = sess.Write(buf) // echo
		srvErr <- err
	}()

	// Dialer: a plain kcp.Dial (binds its own socket) to the shared socket's addr.
	sess, err := kcp.Dial(master.LocalAddr().String())
	if err != nil {
		t.Fatalf("kcp.Dial to the shared socket (demux must route KCP to sudph): %v", err)
	}
	defer sess.Close() //nolint:errcheck

	_ = sess.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	if _, err := sess.Write([]byte("ping")); err != nil {
		t.Fatalf("dialer write: %v", err)
	}
	_ = sess.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	buf := make([]byte, 4)
	if _, err := io.ReadFull(sess, buf); err != nil {
		t.Fatalf("dialer read echo: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q, want ping", buf)
	}
	if err := <-srvErr; err != nil {
		t.Fatalf("server side: %v", err)
	}
}
