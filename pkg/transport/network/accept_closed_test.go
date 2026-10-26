package network

import (
	"net"
	"testing"
	"time"

	"github.com/soheilhy/cmux"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// closedListener fails every Accept as a closed cmux listener does.
type closedListener struct {
	net.Listener
	err error
}

func (l closedListener) Accept() (net.Conn, error) { return nil, l.err }

// A closed listener ends the accept loop even before the client is closed.
// The loop used to retry and log forever, which held up a visor's shutdown.
func TestAcceptTransportsStopsOnClosedListener(t *testing.T) {
	for _, err := range []error{cmux.ErrListenerClosed, cmux.ErrServerClosed, net.ErrClosed} {
		ln, lerr := net.Listen("tcp", "127.0.0.1:0")
		if lerr != nil {
			t.Fatal(lerr)
		}
		c := newTestGenericClient(t, types.STCPR)
		done := make(chan struct{})
		go func() {
			c.acceptTransports(closedListener{ln, err})
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("accept loop kept running after %v", err)
		}
		ln.Close() //nolint:errcheck,gosec
	}
}
