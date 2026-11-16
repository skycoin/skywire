package call

import (
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestInboundDropsSilentConn opens a signaling conn and sends nothing. The
// handler must give up after inboundSigTimeout instead of holding it.
func TestInboundDropsSilentConn(t *testing.T) {
	old := inboundSigTimeout
	inboundSigTimeout = 100 * time.Millisecond
	t.Cleanup(func() { inboundSigTimeout = old })

	pk, _ := cipher.GenerateKeyPair()
	s := NewSignaler(pk, 0, nil, nil)
	caller, callee := net.Pipe()
	defer caller.Close() //nolint:errcheck

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleInbound(callee)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handleInbound still waits on a silent conn")
	}
}
