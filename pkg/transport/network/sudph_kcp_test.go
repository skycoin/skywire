//go:build !(js && wasm)

package network

import (
	"net"
	"testing"

	kcp "github.com/0magnet/kcp-go/v5"
	"github.com/AudriusButkevicius/pfilter"
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
