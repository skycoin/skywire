package cxoaggregate

import (
	"sync"
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// TestQueueFilledReturnsAndCoalesces: the fill-loop callback never waits on
// the service's apply, and Roots that pile up behind a slow apply collapse
// to the newest per feed.
func TestQueueFilledReturnsAndCoalesces(t *testing.T) {
	c := &Core{
		filled:   make(map[skycipher.PubKey]*registry.Root),
		draining: make(map[skycipher.PubKey]struct{}),
	}
	release := make(chan struct{})
	var mu sync.Mutex
	var got []uint64
	apply := func(r *registry.Root) {
		<-release
		mu.Lock()
		got = append(got, r.Seq)
		mu.Unlock()
	}
	var feed skycipher.PubKey
	feed[0] = 1

	done := make(chan struct{})
	go func() {
		for seq := uint64(1); seq <= 5; seq++ {
			c.queueFilled(&registry.Root{Pub: feed, Seq: seq}, apply)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("queueFilled blocked behind a slow apply")
	}
	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for {
		c.fillMu.Lock()
		idle := len(c.draining) == 0
		c.fillMu.Unlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("drainer never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 || len(got) > 2 || got[len(got)-1] != 5 {
		t.Fatalf("applied seqs = %v, want the first and then only the newest (5)", got)
	}
}
