package node

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/node/msg"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
)

// floodRequests sends n copies of m at once over c and reports how many were
// refused with an Err within a second.
func floodRequests(t *testing.T, c *Conn, m msg.Msg, n int) int {
	t.Helper()
	var refused atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for range n {
		wg.Go(func() {
			reply, err := c.sendRequestTimeout(m, 2*time.Second)
			if _, ok := reply.(*msg.Err); err == nil && ok && time.Since(start) < time.Second {
				refused.Add(1)
			}
		})
	}
	wg.Wait()
	return int(refused.Load())
}

// TestServesAreBoundedPerConn floods a node with RqObject for an object it
// is still waiting for, and RqDelta for a Root it does not have yet. Each
// would park a goroutine for a long wait, so past the cap they are refused.
func TestServesAreBoundedPerConn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("CXO node tests are unreliable on Windows CI runners")
	}
	sconf := getTestConfig("sender")
	rconf := getTestConfig("receiver")
	rconf.TCP.Listen, rconf.UDP.Listen = "", ""

	sn, err := NewNode(sconf)
	assertNil(t, err)
	t.Cleanup(func() { sn.Close() }) //nolint:errcheck,gosec
	rn, err := NewNode(rconf)
	assertNil(t, err)
	t.Cleanup(func() { rn.Close() }) //nolint:errcheck,gosec

	pk, _ := cipher.GenerateKeyPair()
	assertNil(t, sn.Share(pk))

	key := cipher.SumSHA256([]byte("an object a filler waits for"))
	assertNil(t, sn.Container().Want(key, make(chan skyobject.Object, 1), 0))

	c, err := rn.TCP().Connect(sn.TCP().Address())
	assertNil(t, err)
	waitForCondition(t, "sender registered connection", func() bool {
		return len(sn.Connections()) > 0
	})

	const n = 3 * maxObjectServes
	if got := floodRequests(t, c, &msg.RqObject{Key: key}, n); got < n-maxObjectServes {
		t.Errorf("RqObject: %d of %d refused at once, want at least %d", got, n, n-maxObjectServes)
	}
	const nd = 3 * maxDeltaServes
	if got := floodRequests(t, c, &msg.RqDelta{Feed: pk, Seq: 1}, nd); got < nd-maxDeltaServes {
		t.Errorf("RqDelta: %d of %d refused at once, want at least %d", got, nd, nd-maxDeltaServes)
	}
}
