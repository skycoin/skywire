package skyobject

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// fillServing fills r into rc from sc, serving every requested object
// except the failAt-th one (counting from 1), where the fill is broken as
// a timed out request breaks it. failAt 0 serves everything.
func fillServing(t *testing.T, sc, rc *Container, r *registry.Root, failAt int) error {
	t.Helper()
	rq := make(chan cipher.SHA256, 10)
	f := rc.Fill(r, rq, 10)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		n := 0
		for key := range rq {
			if n++; n == failAt {
				go f.Fail(errors.New("object request timed out"))
				continue
			}
			val, _, err := sc.Get(key, 0)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := rc.SetWanted(key, val); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	err := f.Run()
	close(rq)
	wg.Wait()
	return err
}

// Broken fills leave part of a tree stored. A later fill of a Root with
// the same tree must fetch everything still missing below those parts,
// or it counts as full while objects under it are absent. That is how
// every tpd-metrics subscriber stopped syncing on 2026-10-15.
func TestFillAfterBrokenFillsFetchesTheRest(t *testing.T) {
	for _, breaks := range [][]int{{5}, {20}, {60}} {
		for _, dup := range []bool{false, true} {
			t.Run(fmt.Sprintf("breaks=%v/duplicate_leaves=%v", breaks, dup), func(t *testing.T) {
				sc, rc := getTestContainer(), getTestContainer()
				pk, sk := cipher.GenerateKeyPair() //nolint:errcheck,gosec
				assertNil(t, sc.AddFeed(pk))
				assertNil(t, rc.AddFeed(pk))
				up, err := sc.Unpack(sk, testRegistry)
				assertNil(t, err)

				feed := Feed{Head: "days"}
				for i := 0; i < 300; i++ {
					body := fmt.Sprintf("records %d", i)
					if dup && i%2 == 0 {
						body = "[]" // like the empty days on the metrics feed
					}
					assertNil(t, feed.Posts.AppendValues(up, Post{Head: "day", Body: body}))
				}
				r := &registry.Root{Pub: pk, Nonce: 1}
				r.Refs = []registry.Dynamic{createDynamic(up, testRegistry, "test.Feed", &feed)}

				for _, at := range breaks {
					assertNil(t, sc.Save(up, r))
					_ = fillServing(t, sc, rc, r, at) //nolint:errcheck // a fill may finish before it reaches at
				}
				assertNil(t, sc.Save(up, r))
				assertNil(t, fillServing(t, sc, rc, r, 0))

				var want, got int
				assertNil(t, sc.Walk(r, func(cipher.SHA256, int) (bool, error) { want++; return true, nil }))
				err = rc.Walk(r, func(cipher.SHA256, int) (bool, error) { got++; return true, nil })
				if err != nil || got != want {
					t.Fatalf("filled Root walks %d of %d objects: %v", got, want, err)
				}
			})
		}
	}
}
