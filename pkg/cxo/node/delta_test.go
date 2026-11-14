package node

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// deltaFillRun fills two Roots from sender to receiver and returns the
// object requests the sender served for each fill.
func deltaFillRun(t *testing.T, oldSender, oldReceiver bool) (first, second uint64, c *Conn) {
	t.Helper()
	return deltaFillRunOpt(t, oldSender, oldReceiver, false)
}

func deltaFillRunOpt(t *testing.T, oldSender, oldReceiver, spoilFirst bool) (first, second uint64, c *Conn) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("CXO root filling is unreliable on Windows CI runners")
	}

	var (
		fr, onRootFilled   = onRootFilledToChannel(100)
		rr, onRootReceived = onRootReceivedToChannel(1)
		sconf              = getTestConfig("sender")
		rconf              = getTestConfig("receiver")
	)
	rconf.TCP.Listen, rconf.UDP.Listen = "", ""
	rconf.OnRootFilled = onRootFilled
	rconf.OnRootReceived = onRootReceived
	rconf.OnFillingBreaks = onFillingBreaksTestLog(t)

	sn, err := NewNode(sconf)
	assertNil(t, err)
	t.Cleanup(func() { sn.Close() }) //nolint:errcheck,gosec
	rn, err := NewNode(rconf)
	assertNil(t, err)
	t.Cleanup(func() { rn.Close() }) //nolint:errcheck,gosec
	sn.noDelta, rn.noDelta = oldSender, oldReceiver

	pk, sk := cipher.GenerateKeyPair()
	assertNil(t, sn.Share(pk))
	assertNil(t, rn.Share(pk))

	sc := sn.Container()
	up, err := sc.Unpack(sk, getTestRegistry())
	assertNil(t, err)

	r := &registry.Root{Nonce: 9021, Pub: pk}
	var feed Feed
	addPosts := func(from, n int) {
		for i := from; i < from+n; i++ {
			assertNil(t, feed.Posts.AppendValues(up, Post{
				Head: fmt.Sprintf("Head #%d", i),
				Body: fmt.Sprintf("Body #%d", i),
			}))
		}
		r.Refs = []registry.Dynamic{
			dynamicByValue(t, up, "test.User", User{"Alice", 19, nil}),
			dynamicByValue(t, up, "test.Feed", feed),
		}
		assertNil(t, sc.Save(up, r))
	}
	waitFilled := func(seq uint64) {
		t.Helper()
		select {
		case f := <-fr:
			if f.Seq != seq {
				t.Fatalf("filled seq %d, want %d", f.Seq, seq)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for root replication")
		}
	}

	addPosts(0, 32)
	if spoilFirst {
		sc.RecordDelta(pk, r.Nonce, 0, []cipher.SHA256{cipher.SumSHA256([]byte("none"))})
	}

	c, err = rn.TCP().Connect(sn.TCP().Address())
	assertNil(t, err)
	waitForCondition(t, "sender registered connection", func() bool {
		return len(sn.Connections()) > 0
	})
	if !oldReceiver {
		waitForCondition(t, "delta probe answered", func() bool {
			return c.deltaCap.Load() != deltaUnknown
		})
	}

	subscribeWithRetry(t, c, pk, rr, 3)
	waitFilled(0)
	first = sn.objectRequests.Swap(0)

	addPosts(32, 1)
	sn.Publish(r)
	waitFilled(1)
	second = sn.objectRequests.Load()
	return first, second, c
}

func TestDeltaFill(t *testing.T) {
	first, second, c := deltaFillRun(t, false, false)
	if !c.deltaSupported() {
		t.Fatal("new peer not detected as serving RqDelta")
	}
	if first != 0 || second != 0 {
		t.Fatalf("object requests %d and %d, want all objects from the deltas", first, second)
	}
}

func TestDeltaFillOldSender(t *testing.T) {
	first, second, c := deltaFillRun(t, true, false)
	if c.deltaCap.Load() != deltaNo {
		t.Fatalf("old peer cap %d, want deltaNo", c.deltaCap.Load())
	}
	if first == 0 || second == 0 {
		t.Fatalf("object requests %d and %d, want the per-object fill", first, second)
	}
	select {
	case <-c.Done():
		t.Fatal("connection to the old peer closed")
	default:
	}
}

func TestDeltaFillOldReceiver(t *testing.T) {
	first, second, _ := deltaFillRun(t, false, true)
	if first == 0 || second == 0 {
		t.Fatalf("object requests %d and %d, want the per-object fill", first, second)
	}
}

// TestDeltaPartial checks that objects the delta lacks are still fetched
// one by one, and a bogus key in it does no harm.
func TestDeltaPartial(t *testing.T) {
	first, second, _ := deltaFillRunOpt(t, false, false, true)
	if first == 0 || second != 0 {
		t.Fatalf("object requests %d and %d, want the first fill per object", first, second)
	}
}
