// Package visor pkg/visor/embedded_wisp_test.go c3-vis-core
package visor

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/wisp"
)

// freePort returns a port nothing is listening on. On a native visor
// bottle's vnet is net, so the embedded server binds a real socket and the
// test needs a real free port for it.
func freePort(t *testing.T) uint {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close() //nolint:errcheck,gosec // only used to claim a number
	addr, _ := l.Addr().(*net.TCPAddr)
	return uint(addr.Port) //nolint:gosec // a bound port is in range
}

// echoServer answers each connection with the bytes it receives, uppercased.
func echoServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() }) //nolint:errcheck,gosec // test teardown

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close() //nolint:errcheck,gosec // test far end
				buf := make([]byte, 512)
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				c.Write(bytes.ToUpper(buf[:n])) //nolint:errcheck,gosec // test far end
			}()
		}
	}()
	return l.Addr().String()
}

// connectSocks serves a SOCKS5 proxy that honors CONNECT and nothing else —
// the shape skysocks-client presents to the embedded server.
func connectSocks(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() }) //nolint:errcheck,gosec // test teardown

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go serveOneConnect(c)
		}
	}()
	return l.Addr().String()
}

func serveOneConnect(c net.Conn) {
	defer c.Close() //nolint:errcheck,gosec // test proxy

	head := make([]byte, 2)
	if _, err := io.ReadFull(c, head); err != nil {
		return
	}
	if _, err := io.ReadFull(c, make([]byte, int(head[1]))); err != nil {
		return
	}
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil {
		return
	}
	var host string
	switch req[3] {
	case 0x01:
		b := make([]byte, 4)
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 0x03:
		var l [1]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			return
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = string(b)
	default:
		return
	}
	var pb [2]byte
	if _, err := io.ReadFull(c, pb[:]); err != nil {
		return
	}
	if req[1] != 0x01 { // CONNECT only, as skysocks was before UDP ASSOCIATE
		c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) //nolint:errcheck,gosec // test proxy
		return
	}

	target, err := net.Dial("tcp", net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(pb[:]))))
	if err != nil {
		c.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) //nolint:errcheck,gosec // test proxy
		return
	}
	defer target.Close() //nolint:errcheck,gosec // test proxy

	if _, err := c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go io.Copy(target, c) //nolint:errcheck,gosec // test proxy
	io.Copy(c, target)    //nolint:errcheck,gosec // test proxy
}

// TestEmbeddedWispServesASessionOnItsPort is the wiring end to end: the
// embedded server binds the virtual-loopback port, a client dials it with no
// WebSocket anywhere, and the stream comes out of the configured proxy.
func TestEmbeddedWispServesASessionOnItsPort(t *testing.T) {
	echo := echoServer(t)
	proxyAddr := connectSocks(t)
	port := freePort(t)

	w := newEmbeddedWisp(&visorconfig.WispConfig{
		Enable:        true,
		Port:          port,
		UpstreamSOCKS: proxyAddr,
	}, logging.MustGetLogger("wisp-test"))

	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { w.Stop() }) //nolint:errcheck,gosec // test teardown

	if !w.Running() {
		t.Fatal("Running() = false after a successful Start")
	}
	if got := w.Addr(); got != fmt.Sprintf("127.0.0.1:%d", port) {
		t.Fatalf("Addr() = %q, want 127.0.0.1:%d", got, port)
	}

	conn, err := net.Dial("tcp", w.Addr())
	if err != nil {
		t.Fatalf("dial the wisp port: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := wisp.DialConn(ctx, conn, wisp.ClientConfig{URL: "vnet"})
	if err != nil {
		t.Fatalf("DialConn: %v", err)
	}
	defer c.Close() //nolint:errcheck,gosec // test teardown

	if c.Version() != 2 {
		t.Fatalf("Version() = %d, want 2", c.Version())
	}

	host, portStr, err := net.SplitHostPort(echo)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	var echoPort uint16
	fmt.Sscanf(portStr, "%d", &echoPort) //nolint:errcheck,gosec // a listener's port parses

	stream, err := c.DialTCP(ctx, host, echoPort)
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	if _, err := stream.Write([]byte("through the visor")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := make([]byte, len("THROUGH THE VISOR"))
	stream.SetReadDeadline(time.Now().Add(15 * time.Second)) //nolint:errcheck,gosec // test
	if _, err := io.ReadFull(stream, got); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(got) != "THROUGH THE VISOR" {
		t.Fatalf("echo = %q, want %q", got, "THROUGH THE VISOR")
	}
}

func TestEmbeddedWispStopReleasesThePort(t *testing.T) {
	port := freePort(t)
	w := newEmbeddedWisp(&visorconfig.WispConfig{
		Enable:        true,
		Port:          port,
		UpstreamSOCKS: "127.0.0.1:1",
	}, logging.MustGetLogger("wisp-test"))

	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := w.Start(); err == nil {
		t.Fatal("a second Start returned nil error, want one")
	}
	if err := w.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if w.Running() {
		t.Fatal("Running() = true after Stop")
	}
	if err := w.Stop(); err != nil {
		t.Fatalf("a second Stop returned %v, want nil", err)
	}

	// The port must be free again, which is what makes a stop/start cycle
	// from RPC usable rather than a one-way door.
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("the port was not released: %v", err)
	}
	l.Close() //nolint:errcheck,gosec // test teardown
}

func TestEmbeddedWispDefaults(t *testing.T) {
	w := newEmbeddedWisp(&visorconfig.WispConfig{}, logging.MustGetLogger("wisp-test"))
	if got := w.port(); got != visorconfig.DefaultWispPort {
		t.Fatalf("port() = %d, want %d", got, visorconfig.DefaultWispPort)
	}
	if got := w.upstream(); got != "127.0.0.1:1080" {
		t.Fatalf("upstream() = %q, want the local skysocks-client", got)
	}

	w = newEmbeddedWisp(&visorconfig.WispConfig{Port: 7, UpstreamSOCKS: "1.2.3.4:9"}, logging.MustGetLogger("wisp-test"))
	if got := w.port(); got != 7 {
		t.Fatalf("port() = %d, want the configured 7", got)
	}
	if got := w.upstream(); got != "1.2.3.4:9" {
		t.Fatalf("upstream() = %q, want the configured proxy", got)
	}
}

// TestInitEmbeddedWispSkipsAnAbsentSection keeps a config written before this
// existed booting exactly as it did.
func TestInitEmbeddedWispSkipsAnAbsentSection(t *testing.T) {
	v := &Visor{conf: &visorconfig.V1{}, initLock: new(sync.RWMutex)}
	if err := initEmbeddedWisp(context.Background(), v, logging.MustGetLogger("wisp-test")); err != nil {
		t.Fatalf("initEmbeddedWisp: %v", err)
	}
	if v.embeddedWisp != nil {
		t.Fatal("a runtime was constructed for an absent wisp section")
	}
}

// TestInitEmbeddedWispConstructsWithoutStarting covers enable=false: the
// runtime exists so it can be toggled later, but nothing is bound.
func TestInitEmbeddedWispConstructsWithoutStarting(t *testing.T) {
	v := &Visor{conf: &visorconfig.V1{Wisp: &visorconfig.WispConfig{Enable: false, Port: freePort(t)}}, initLock: new(sync.RWMutex)}
	if err := initEmbeddedWisp(context.Background(), v, logging.MustGetLogger("wisp-test")); err != nil {
		t.Fatalf("initEmbeddedWisp: %v", err)
	}
	if v.embeddedWisp == nil {
		t.Fatal("no runtime was constructed for enable=false")
	}
	if v.embeddedWisp.Running() {
		t.Fatal("the server bound a port despite enable=false")
	}
}
