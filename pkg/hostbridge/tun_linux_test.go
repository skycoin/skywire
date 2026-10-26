//go:build linux

// Package hostbridge pkg/hostbridge/tun_linux_test.go c3-vis-wasm
package hostbridge

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

// TestTUN needs CAP_NET_ADMIN in its own network namespace, so it runs as
//
//	go test -c -o hb.test ./pkg/hostbridge
//	unshare -Urn sh -c "ip link set lo up; ./hb.test -test.run TUN"
//
// and skips anywhere it cannot open a TUN.
func TestTUN(t *testing.T) {
	if fd, err := syscall.Open("/dev/net/tun", syscall.O_RDWR, 0); err != nil {
		t.Skip("no /dev/net/tun:", err)
	} else {
		_ = syscall.Close(fd) //nolint:errcheck
	}
	// It replaces the default route, so never on a real network.
	if gw, err := defaultGateway(); err == nil {
		t.Skip("this network has a default route via " + gw + "; run the test in an empty namespace")
	}
	c := connect(t, startBridge(t))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tun, err := c.TUN(ctx)
	if errors.Is(err, syscall.EPERM) || (err != nil && err.Error() == syscall.EPERM.Error()) {
		t.Skip("no CAP_NET_ADMIN; run the test under unshare -Urn")
	}
	must(t, err)
	if _, err := c.Net(ctx, "setup", "lo", "10.77.0.9/29", "1500"); err == nil {
		t.Fatal("set up a device the bridge did not open")
	}
	_, err = c.Net(ctx, "setup", tun.Name(), "10.77.0.2/29", "1400")
	must(t, err)
	_, err = c.Net(ctx, "route", "add", "10.88.0.0/16", "10.77.0.1")
	must(t, err)
	_, err = c.Net(ctx, "route", "change", "0.0.0.0/0", "10.77.0.1")
	must(t, err)
	gw, err := c.Net(ctx, "gateway")
	must(t, err)
	if gw != "10.77.0.1" {
		t.Fatalf("gateway %q", gw)
	}

	// A datagram to the routed network comes out of the TUN, and a reply
	// written into it reaches the sender.
	u, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(10, 88, 0, 5), Port: 9999})
	must(t, err)
	defer u.Close() //nolint:errcheck
	_, err = u.Write([]byte("ping"))
	must(t, err)
	pkt := make([]byte, 2000)
	for {
		n, err := tun.Read(pkt)
		must(t, err)
		if n >= 28 && pkt[9] == syscall.IPPROTO_UDP && net.IP(pkt[16:20]).Equal(net.IPv4(10, 88, 0, 5)) {
			if string(pkt[28:n]) != "ping" {
				t.Fatalf("payload %q", pkt[28:n])
			}
			break
		}
	}
	local := u.LocalAddr().(*net.UDPAddr)
	_, err = tun.Write(udpPacket(net.IPv4(10, 88, 0, 5), 9999, local.IP, local.Port, []byte("pong")))
	must(t, err)
	_ = u.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	b := make([]byte, 16)
	n, err := u.Read(b)
	must(t, err)
	if string(b[:n]) != "pong" {
		t.Fatalf("reply %q", b[:n])
	}

	_, err = c.Net(ctx, "route", "del", "10.88.0.0/16", "10.77.0.1")
	must(t, err)
	name := tun.Name()
	must(t, tun.Close())
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := net.InterfaceByName(name); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still exists after close", name)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func udpPacket(src net.IP, sport int, dst net.IP, dport int, payload []byte) []byte {
	p := make([]byte, 28+len(payload))
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:], uint16(len(p))) //nolint:gosec // a test packet
	p[8] = 64
	p[9] = syscall.IPPROTO_UDP
	copy(p[12:16], src.To4())
	copy(p[16:20], dst.To4())
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(p[i:]))
	}
	for sum > 0xffff {
		sum = sum&0xffff + sum>>16
	}
	binary.BigEndian.PutUint16(p[10:], ^uint16(sum))
	binary.BigEndian.PutUint16(p[20:], uint16(sport))          //nolint:gosec // a test port
	binary.BigEndian.PutUint16(p[22:], uint16(dport))          //nolint:gosec // a test port
	binary.BigEndian.PutUint16(p[24:], uint16(8+len(payload))) //nolint:gosec // a test packet
	copy(p[28:], payload)
	return p
}
