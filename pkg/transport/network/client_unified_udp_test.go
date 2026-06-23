//go:build !tinygo

package network

import (
	"net"
	"testing"
)

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe port: %v", err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	c.Close() //nolint:errcheck,gosec
	return port
}

// TestClientFactory_UnifiedUDP verifies the opt-in: off by default, a no-op for
// port 0, and once enabled it exposes a shared demux conn for the UDP types.
func TestClientFactory_UnifiedUDP(t *testing.T) {
	f := &ClientFactory{}
	if f.sharedUDPConn(protoQUIC) != nil {
		t.Fatal("shared conn should be nil before enable")
	}
	if err := f.EnableUnifiedUDP(0); err != nil {
		t.Fatalf("EnableUnifiedUDP(0): %v", err)
	}
	if f.sharedUDPConn(protoQUIC) != nil {
		t.Fatal("port 0 must be a no-op")
	}

	port := freeUDPPort(t)
	if err := f.EnableUnifiedUDP(port); err != nil {
		t.Fatalf("EnableUnifiedUDP(%d): %v", port, err)
	}
	defer f.CloseUnifiedUDP() //nolint:errcheck

	qc := f.sharedUDPConn(protoQUIC)
	sc := f.sharedUDPConn(protoSUDPH)
	if qc == nil || sc == nil {
		t.Fatal("quic + sudph shared conns must be non-nil when enabled")
	}
	if qc.LocalAddr().String() != sc.LocalAddr().String() {
		t.Fatalf("quic and sudph must share one socket: %s vs %s", qc.LocalAddr(), sc.LocalAddr())
	}
}
