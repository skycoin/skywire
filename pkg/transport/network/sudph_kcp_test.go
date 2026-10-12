//go:build !(js && wasm)

package network

import (
	"io"
	"net"
	"testing"
	"time"

	kcp "github.com/0magnet/kcp-go/v5"
	"github.com/0magnet/pfilter"
)

// kcp-go v5.6 panicked building batch IO on a pfilter conn, which has
// ReadMsgUDP but is not a net.Conn.
func TestKCPOverPacketFilter(t *testing.T) {
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	filter := pfilter.NewPacketFilter(udp)
	filter.Start()
	defer udp.Close() //nolint:errcheck

	sess, err := kcp.NewConn("127.0.0.1:9", nil, 0, 0, plainPacketConn(filter.NewConn(10, nil)))
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Close() //nolint:errcheck

	l, err := kcp.ServeConn(nil, 0, 0, plainPacketConn(filter.NewConn(20, nil)))
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close() //nolint:errcheck
}

// A dialed session and a listener get their packets from the filter's read
// goroutine.
func TestKCPDialOverPacketFilterPushes(t *testing.T) {
	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close() //nolint:errcheck
	srvFilter := pfilter.NewPacketFilter(srv)
	l, err := kcp.ServeConn(nil, 0, 0, pushPacketConn(srvFilter.NewConn(20, nil)))
	if err != nil {
		t.Fatal(err)
	}
	srvFilter.Start()
	defer l.Close() //nolint:errcheck
	go func() {
		c, err := l.AcceptKCP()
		if err != nil {
			return
		}
		_, _ = io.Copy(c, c) //nolint:errcheck
	}()

	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close() //nolint:errcheck
	filter := pfilter.NewPacketFilter(udp)
	conn := pushPacketConn(filter.NewConn(10, nil))
	filter.Start()
	if _, ok := conn.(kcp.PacketPusher); !ok {
		t.Fatal("dial conn hides the packet receiver")
	}

	sess, err := kcp.NewConn(srv.LocalAddr().String(), nil, 0, 0, conn)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close() //nolint:errcheck
	if _, err := sess.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	_ = sess.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	buf := make([]byte, 5)
	if _, err := io.ReadFull(sess, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("echo %q, %v", buf, err)
	}
}
