//go:build linux && !tinygo

package network

import (
	"context"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func countSocketFDs(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("no /proc/self/fd")
	}
	n := 0
	for _, e := range ents {
		if l, err := os.Readlink("/proc/self/fd/" + e.Name()); err == nil && strings.HasPrefix(l, "socket:") {
			n++
		}
	}
	return n
}

// TestWebRTCPeerConnectionsShareSockets opens several dial/accept pairs and
// checks that they share sockets instead of binding their own, and that each
// conn reports the selected candidate's IP as its remote address.
func TestWebRTCPeerConnectionsShareSockets(t *testing.T) {
	const pairs = 8
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sockBefore, grBefore := countSocketFDs(t), runtime.NumGoroutine()
	var conns []net.Conn
	for i := 0; i < pairs; i++ {
		sigA, sigB := net.Pipe()
		var a, b net.Conn
		var aErr, bErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); a, aErr = webrtcDial(ctx, sigA, nil, nil) }()
		go func() { defer wg.Done(); b, bErr = webrtcAccept(ctx, sigB, nil, nil) }()
		wg.Wait()
		if aErr != nil || bErr != nil {
			t.Fatalf("pair %d: dial %v, accept %v", i, aErr, bErr)
		}
		conns = append(conns, a, b)
	}
	defer func() {
		for _, c := range conns {
			c.Close() //nolint:errcheck,gosec
		}
	}()
	sockets := countSocketFDs(t) - sockBefore
	goroutines := runtime.NumGoroutine() - grBefore
	t.Logf("%d pairs: %d UDP sockets, %d goroutines", pairs, sockets, goroutines)

	for _, c := range conns {
		u, ok := c.RemoteAddr().(*net.UDPAddr)
		if !ok {
			t.Fatalf("RemoteAddr = %T %v, want *net.UDPAddr", c.RemoteAddr(), c.RemoteAddr())
		}
		if _, ok := c.LocalAddr().(*net.UDPAddr); !ok {
			t.Fatalf("LocalAddr = %T, want *net.UDPAddr", c.LocalAddr())
		}
		if u.IP.IsLoopback() || u.IP.IsLinkLocalUnicast() {
			t.Fatalf("selected a filtered address %v", u)
		}
	}
	if sockets >= pairs {
		t.Fatalf("%d sockets for %d pairs, want them shared", sockets, pairs)
	}
}

func TestKeepICEInterface(t *testing.T) {
	for name, want := range map[string]bool{
		"eth0": true, "enp3s0": true, "wlan0": true, "lo": true,
		"docker0": false, "br-1a2b3c": false, "veth12ab": false, "skydns0": false, "tun0": false,
	} {
		if got := keepICEInterface(name); got != want {
			t.Errorf("keepICEInterface(%q) = %v, want %v", name, got, want)
		}
	}
	for ip, want := range map[string]bool{
		"192.168.1.5": true, "203.0.113.7": true, "2001:db8::1": true,
		"169.254.10.1": false, "fe80::1": false, "127.0.0.1": false,
	} {
		if got := keepICEIP(net.ParseIP(ip)); got != want {
			t.Errorf("keepICEIP(%s) = %v, want %v", ip, got, want)
		}
	}
}
