//go:build !tinygo

package network

import (
	"net"
	"testing"
)

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close() //nolint:errcheck,gosec
	return port
}

// TestClientFactory_UnifiedTCP verifies the opt-in: off by default, a no-op for
// port 0, and once enabled it exposes the stcpr + WS virtual listeners over one
// TCP socket.
func TestClientFactory_UnifiedTCP(t *testing.T) {
	f := &ClientFactory{}
	if f.stcprSharedListener != nil || f.wsSharedListener != nil {
		t.Fatal("shared listeners should be nil before enable")
	}
	if err := f.EnableUnifiedTCP(0); err != nil {
		t.Fatalf("EnableUnifiedTCP(0): %v", err)
	}
	if f.stcprSharedListener != nil {
		t.Fatal("port 0 must be a no-op")
	}

	port := freeTCPPort(t)
	if err := f.EnableUnifiedTCP(port); err != nil {
		t.Fatalf("EnableUnifiedTCP(%d): %v", port, err)
	}
	defer f.CloseUnifiedTCP() //nolint:errcheck

	if f.stcprSharedListener == nil || f.wsSharedListener == nil {
		t.Fatal("stcpr + WS virtual listeners must be non-nil when enabled")
	}
	if f.stcprSharedListener.Addr().String() != f.wsSharedListener.Addr().String() {
		t.Fatalf("stcpr and WS must share one socket: %s vs %s",
			f.stcprSharedListener.Addr(), f.wsSharedListener.Addr())
	}
}
