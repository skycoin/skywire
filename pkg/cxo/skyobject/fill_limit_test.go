package skyobject

import (
	"fmt"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// TestFillParallelLimit checks that a fill never waits on more objects at
// once than its parallelism allows: maxParall goroutines plus the one Run
// walks inline on.
func TestFillParallelLimit(t *testing.T) {
	sc := getTestContainer()
	pk, sk := cipher.GenerateKeyPair()
	assertNil(t, sc.AddFeed(pk))

	up, err := sc.Unpack(sk, testRegistry)
	assertNil(t, err)
	r := &registry.Root{Pub: pk, Nonce: 1}
	feed := Feed{Head: "h"}
	for i := range 300 {
		assertNil(t, feed.Posts.AppendValues(up, Post{Head: fmt.Sprint(i)}))
	}
	r.Refs = []registry.Dynamic{
		createDynamic(up, testRegistry, "test.Feed", &feed),
		createDynamic(up, testRegistry, "test.User", &User{Name: "a"}),
		createDynamic(up, testRegistry, "test.User", &User{Name: "b"}),
	}
	assertNil(t, sc.Save(up, r))

	for _, mp := range []int{1, 2, 4} {
		t.Run(fmt.Sprint(mp), func(t *testing.T) {
			rc := getTestContainer()
			assertNil(t, rc.AddFeed(pk))
			rq := make(chan cipher.SHA256, 1024)
			f := rc.Fill(r, rq, mp)

			peak := make(chan int, 1)
			go func() {
				var batch []cipher.SHA256
				most := 0
				for {
					select {
					case key, ok := <-rq:
						if !ok {
							peak <- most
							return
						}
						batch = append(batch, key)
						continue
					case <-time.After(10 * time.Millisecond):
					}
					most = max(most, len(batch))
					for _, key := range batch {
						val, _, gerr := sc.Get(key, 0)
						if gerr == nil {
							_, _ = rc.SetWanted(key, val) //nolint:errcheck
						}
					}
					batch = batch[:0]
				}
			}()

			assertNil(t, f.Run())
			close(rq)
			if got := <-peak; got > mp+1 {
				t.Fatalf("%d objects waited on at once, want at most %d", got, mp+1)
			}
		})
	}
}
