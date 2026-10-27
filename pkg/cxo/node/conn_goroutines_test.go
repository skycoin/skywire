package node

import (
	"bytes"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
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

// headGoroutines counts goroutines in the head loop.
func headGoroutines(t *testing.T) int {
	t.Helper()
	var b bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&b, 2); err != nil {
		t.Fatal(err)
	}
	return strings.Count(b.String(), "cxo/node.(*nodeHead).handle(")
}

// A head runs a goroutine only while it fills a Root, and starts it again for
// the next one.
func TestIdleHeadHoldsNoGoroutine(t *testing.T) {
	var (
		fr, onRootFilled = onRootFilledToChannel(10)
		sn               = getTestNode("head-sender")
		rconf            = getTestConfigNotListen("head-receiver")
	)
	rconf.OnRootFilled = onRootFilled
	rconf.OnFillingBreaks = onFillingBreaksTestLog(t)
	rn, err := NewNode(rconf)
	assertNil(t, err)
	defer sn.Close() //nolint:errcheck,gosec
	defer rn.Close() //nolint:errcheck,gosec

	pk, sk := cipher.GenerateKeyPair()
	assertNil(t, sn.Share(pk))
	assertNil(t, rn.Share(pk))
	sc := sn.Container()
	up, err := sc.Unpack(sk, getTestRegistry())
	assertNil(t, err)

	c, err := rn.TCP().Connect(sn.TCP().Address())
	assertNil(t, err)
	waitForCondition(t, "sender registered connection", func() bool {
		return len(sn.Connections()) > 0
	})
	assertNil(t, c.Subscribe(pk))

	for i, name := range []string{"Alice", "Bob"} {
		r := &registry.Root{Nonce: 9021, Pub: pk}
		r.Refs = append(r.Refs, dynamicByValue(t, up, "test.User", User{name, uint32(i), nil}))
		assertNil(t, sc.Save(up, r))
		sn.Publish(r)
		select {
		case <-fr:
		case <-time.After(30 * time.Second):
			t.Fatalf("root %d not filled", i)
		}
		waitForCondition(t, "idle head without goroutine", func() bool {
			return headGoroutines(t) == 0
		})
	}
}
