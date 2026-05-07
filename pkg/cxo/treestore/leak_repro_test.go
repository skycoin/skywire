package treestore

import (
	"crypto/rand"
	"runtime"
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/logging"
)

func mustLogger() *logging.Logger {
	return logging.MustGetLogger("test")
}

// TestPublisherSyncCleanupBounded drives publishRoot synchronously
// (no goroutines) with cleanup between each publish, and asserts that
// CXDS volume stays bounded under repeated publication.
//
// This is the control case for TestPublisherAsyncCleanupBounded — it
// proves the cleanup logic is correct in isolation.
func TestPublisherSyncCleanupBounded(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	_ = pk
	n := nopNode(t)
	skyPK := skycipher.PubKey(pk)
	if err := n.Share(skyPK); err != nil {
		t.Fatalf("Share: %v", err)
	}
	p := &Publisher{
		log: mustLogger(), cxoNode: n, pk: pk, sk: sk,
		root:         newMemNode(),
		allow:        newAllowState(nil),
		batchWindow:  10 * time.Millisecond,
		wakeup:       make(chan struct{}, 1),
		done:         make(chan struct{}),
		cleanupNudge: make(chan struct{}, 1),
		cleanupDone:  make(chan struct{}),
	}
	close(p.cleanupDone)

	cxds := p.cxoNode.Container().DB().CXDS()
	mkRandom := func(size int) []byte {
		b := make([]byte, size)
		_, _ = rand.Read(b)
		return b
	}

	publishOnce := func() {
		if err := p.Put("uptimes/days/30", mkRandom(2*1024*1024)); err != nil {
			t.Fatal(err)
		}
		if err := p.publishIfDirty(); err != nil {
			t.Fatalf("publishIfDirty: %v", err)
		}
	}
	cleanup := func() {
		c := p.cxoNode.Container()
		if err := cxoutils.RemoveRootObjects(c, 1); err != nil {
			t.Fatalf("RemoveRootObjects: %v", err)
		}
		if err := cxoutils.RemoveObjects(c); err != nil {
			t.Fatalf("RemoveObjects: %v", err)
		}
	}

	publishOnce()
	cleanup()
	all0, _ := cxds.Amount()
	vol0, _ := cxds.Volume()

	for i := 1; i < 20; i++ {
		publishOnce()
		cleanup()
	}
	all1, _ := cxds.Amount()
	vol1, _ := cxds.Volume()
	t.Logf("baseline: amount=%d volume=%d  after 20 cycles: amount=%d volume=%d",
		all0, vol0, all1, vol1)

	if all1 > all0+5 {
		t.Errorf("amount grew from %d to %d over 19 cycles", all0, all1)
	}
	if vol1 > vol0+1024*1024 {
		t.Errorf("volume grew from %d to %d over 19 cycles", vol0, vol1)
	}
	runtime.GC()
}

// TestPublisherAsyncCleanupBounded reproduces the production leak
// scenario: the runLoop publishes async, the runCleanupLoop processes
// nudges async, and tree dedup creates shared TreeEntry hashes
// between consecutive Roots. The pre-fix bug was in
// (*Index).delPackWalkFunc — when DelRoot's walk reached a leaf
// TreeEntry's empty Sub field (Ref.Hash == 0), getNoCache returned
// ErrNotFound and aborted the walk before visiting any sibling
// leaves. Result: rcs on shared TreeEntry hashes were never
// decremented, RemoveObjects could never sweep them, and CXDS
// accumulated ~one tick's worth of leaked bytes per publish.
func TestPublisherAsyncCleanupBounded(t *testing.T) {
	p, _ := newTestPublisher(t)
	cxds := p.cxoNode.Container().DB().CXDS()

	mkRandom := func(size int) []byte {
		b := make([]byte, size)
		_, _ = rand.Read(b)
		return b
	}

	stableValue := mkRandom(2 * 1024 * 1024)
	for i := 0; i < 30; i++ {
		// Constant path — same value every tick. Its TreeEntry hash
		// dedups with prior roots; this is exactly the production
		// pattern (uptime metrics where one window is unchanged).
		if err := p.Put("uptimes/days/7", stableValue); err != nil {
			t.Fatal(err)
		}
		if err := p.Put("uptimes/days/30", mkRandom(2*1024*1024)); err != nil {
			t.Fatal(err)
		}
		if err := p.Flush(); err != nil {
			t.Fatal(err)
		}
		// Let the cleanup goroutine drain its nudge before the next tick.
		time.Sleep(20 * time.Millisecond)
	}

	all, _ := cxds.Amount()
	vol, _ := cxds.Volume()
	rcHist := map[uint32]int{}
	if err := cxds.Iterate(func(_ skycipher.SHA256, rc uint32, _ []byte) error {
		rcHist[rc]++
		return nil
	}); err != nil {
		t.Fatalf("Iterate: %v", err)
	}
	t.Logf("After 30 ticks: amount=%d volume=%d rcHist=%v", all, vol, rcHist)

	// One stable Root references ~10–12 small structural objects plus
	// 2 large leaf TreeEntries. Total volume bound: ~6 MB. Anything
	// larger means the per-tick leak returned.
	if vol > 8*1024*1024 {
		t.Errorf("CXDS volume %d > 8 MB — async cleanup leak (per-Root retention should be ~6 MB)", vol)
	}
}
