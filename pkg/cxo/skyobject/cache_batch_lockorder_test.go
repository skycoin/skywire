// Package skyobject pkg/cxo/skyobject/cache_batch_lockorder_test.go
//
// Pins the lock order between Cache.mx and bbolt's writer lock, which
// WithBatch is the only place able to invert (it opens the writer, then
// calls back into Cache.Set). The inversion deadlocked a node
// permanently — the publisher stopped clearing dirty, every later send
// was silently dropped, and teardown hung because the cleanup sweep
// wants Cache.mx too. Windows CI hit it as a 25-minute package timeout
// in cmd/apps/skychat/pairing, which publishes and subscribes on one
// CXO node by design.
//
// This test drives the interleaving directly rather than waiting for a
// pairing suite to get unlucky: hold the writer, park a cache reader
// that needs it (a refcount bump goes through a write tx), then start
// the batch on top of both.
package skyobject

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

// lockOrderRounds — one round is enough against the pre-fix code (the
// staging in lockOrderRound is deliberate, not a race to lose), but the
// staging leans on two short sleeps, so a few rounds keep it honest on a
// loaded runner where one of them might land wrong.
const lockOrderRounds = 5

// lockOrderWatchdog bounds the whole run. Far above the ~1s the rounds
// take when nothing is wedged — it only has to tell "slow CI" from
// "blocked forever".
const lockOrderWatchdog = 30 * time.Second

// TestWithBatch_DoesNotInvertCacheAndWriterLocks fails (by watchdog) if
// WithBatch ever again takes bbolt's writer lock before Cache.mx.
func TestWithBatch_DoesNotInvertCacheAndWriterLocks(t *testing.T) {
	// An on-disk container: the in-memory CXDS has no writer lock, so
	// the inversion this guards against cannot exist there.
	conf := NewConfig()
	conf.InMemoryDB = false
	conf.DBPath = filepath.Join(t.TempDir(), "db")

	c, err := NewContainer(conf)
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < lockOrderRounds; i++ {
			lockOrderRound(t, c, i)
		}
	}()

	select {
	case <-done:
	case <-time.After(lockOrderWatchdog):
		// Deliberately no Close() on this path: the wedged goroutines
		// hold the bbolt writer, and bolt's Close waits for it — the
		// cleanup would hang exactly like the bug under test.
		t.Fatalf("deadlock: WithBatch and a concurrent Cache.Get are waiting on each other "+
			"(Cache.mx vs the bbolt writer) — %s elapsed with rounds unfinished", lockOrderWatchdog)
	}

	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// lockOrderRound stages the three-goroutine interleaving that produced
// the CI hang, then lets it resolve:
//
//	(a) holds bbolt's writer lock,
//	(b) WithBatch — wants the writer, and once inside wants Cache.mx,
//	(c) a Cache.Get with inc != 0 — takes Cache.mx, then needs the
//	    writer for the refcount bump, so it parks holding the mutex.
//
// Order matters, and it is why (b) is started before (c): bbolt hands a
// contended writer lock to the goroutine that queued first, so whoever
// waits first wins it when (a) lets go. With (b) first, the pre-fix code
// took the writer, pinned, and only then asked for Cache.mx — which (c)
// was holding while waiting for the writer (b) had just taken. Cycle.
//
// Start them the other way round and the pre-fix code survives by luck:
// (c)'s refcount bump commits before (b) ever enters the tx. That
// accident is why this bug reached CI as an occasional 25-minute hang
// instead of a reproducible failure.
//
// Post-fix (b) takes Cache.mx before the writer, so it simply queues
// behind (c) — no interleaving of the three can cycle.
func lockOrderRound(t *testing.T, c *Container, round int) {
	t.Helper()

	// A key that is on disk but NOT in the cache, so Get has to reach
	// bbolt. Fresh per round: once round N's Get lands the object in
	// the cache, a repeat would short-circuit before the DB.
	key := cipher.SumSHA256([]byte(fmt.Sprintf("lock-order-probe-%d", round)))
	if _, err := c.DB().CXDS().Set(key, []byte("probe"), 1); err != nil {
		t.Errorf("seed CXDS: %v", err)
		return
	}

	var wg sync.WaitGroup
	holding, release := make(chan struct{}), make(chan struct{})

	// (a) Occupy the writer lock so (b) and (c) both have to wait.
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := c.DB().CXDS().RunBatch(func(_ data.CXDS) error {
			close(holding)
			<-release
			return nil
		})
		if err != nil {
			t.Errorf("holder RunBatch: %v", err)
		}
	}()
	<-holding

	// (b) The publisher's batch. Queues on the writer first, so it is
	// the one that gets it.
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := c.WithBatch(func() error {
			k := cipher.SumSHA256([]byte(fmt.Sprintf("lock-order-batch-%d", round)))
			_, err := c.Set(k, []byte("batched"), 1)
			return err
		})
		if err != nil {
			t.Errorf("WithBatch: %v", err)
		}
	}()
	// No signal exists for "now blocked on the writer", so give it a
	// moment. A too-short wait only weakens the round; it cannot cause a
	// false failure, since a correct WithBatch never deadlocks either
	// way.
	time.Sleep(100 * time.Millisecond)

	// (c) The cache reader. inc != 0 makes it a read-modify-write, which
	// bbolt serves from a write tx — so it parks with Cache.mx held.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// The returned value and error don't matter — the locks this
		// call takes on its way to bbolt are the whole point.
		_, _, _ = c.Get(key, 1) //nolint:errcheck,gosec // see above
	}()
	time.Sleep(100 * time.Millisecond)

	close(release)
	wg.Wait()
}
