package node

import (
	"bytes"
	"runtime/pprof"
	"strings"
	"testing"
	"time"
)

// connGoroutines counts goroutines running CXO connection code.
func connGoroutines(t *testing.T) int {
	t.Helper()
	var b bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&b, 2); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, g := range strings.Split(b.String(), "\n\n") {
		if strings.Contains(g, "cxo/node.(*Conn)") || strings.Contains(g, "cxo/node/transport.(*Connection)") {
			n++
		}
	}
	return n
}

// An idle connection holds one goroutine per end: the receive loop. The
// writer runs only while frames are queued.
func TestIdleConnHoldsOneGoroutine(t *testing.T) {
	const clients = 10
	ln := getTestNode("goroutines-server")
	defer ln.Close() //nolint:errcheck,gosec
	for i := 0; i < clients; i++ {
		cn, err := NewNode(getTestConfigNotListen("goroutines-client"))
		assertNil(t, err)
		defer cn.Close() //nolint:errcheck,gosec
		_, err = cn.TCP().Connect(ln.TCP().Address())
		assertNil(t, err)
	}
	waitForCondition(t, "server sees every client", func() bool {
		return len(ln.Connections()) == clients
	})

	ends := 2 * clients
	deadline := time.Now().Add(TM)
	for {
		n := connGoroutines(t)
		if n <= ends {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines in conn code for %d idle conn ends, want at most %d", n, ends, ends)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
