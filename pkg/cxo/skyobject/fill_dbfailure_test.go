// Package skyobject pkg/cxo/skyobject/fill_dbfailure_test.go: pins that
// a real DB failure while filling fails that one filling instead of
// exiting the process.
//
// Pre-fix Filler.get called log.Fatalln on any Get error other than
// data.ErrNotFound. A desktop visor merely restarted; the iOS core runs
// in-process, so the same line killed the app (and the VPN tunnel with it).

package skyobject

import (
	"errors"
	"sync"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/data/cxds"
	"github.com/skycoin/skywire/pkg/cxo/data/idxdb"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

var errDiskGone = errors.New("disk gone")

// getFailingCXDS fails every Get of one key with errDiskGone.
type getFailingCXDS struct {
	data.CXDS
	fail cipher.SHA256
}

func (g *getFailingCXDS) Get(
	key cipher.SHA256,
	inc int,
) (
	val []byte,
	rc uint32,
	err error,
) {
	if key == g.fail {
		return nil, 0, errDiskGone
	}
	return g.CXDS.Get(key, inc)
}

func TestFiller_DBFailure_FailsFillingNotProcess(t *testing.T) {

	var sc = getTestContainer()
	defer sc.Close() //nolint:errcheck,gosec

	var pk, sk = cipher.GenerateKeyPair() //nolint:errcheck,gosec
	assertNil(t, sc.AddFeed(pk))

	var up, err = sc.Unpack(sk, testRegistry)
	assertNil(t, err)

	var r = &registry.Root{Pub: pk, Nonce: 1}
	r.Refs = []registry.Dynamic{
		createDynamic(up, testRegistry, "test.User", &User{
			Name: "Alice",
			Age:  19,
		}),
	}
	assertNil(t, sc.Save(up, r))

	for _, tc := range []struct {
		name string
		key  cipher.SHA256
	}{
		{"registry", cipher.SHA256(r.Reg)}, // read by Run itself
		{"object", r.Refs[0].Hash},         // read by a Split goroutine
	} {
		t.Run(tc.name, func(t *testing.T) {

			var conf = getTestConfig()
			conf.DB = data.NewDB(
				&getFailingCXDS{CXDS: cxds.NewMemoryCXDS(), fail: tc.key},
				idxdb.NewMemeoryDB(),
			)

			var rc, err = NewContainer(conf)
			assertNil(t, err)
			defer rc.Close() //nolint:errcheck,gosec

			assertNil(t, rc.AddFeed(pk))

			var (
				rq = make(chan cipher.SHA256, 10)
				f  = rc.Fill(r, rq, 10)
				wg sync.WaitGroup
			)

			wg.Add(1)
			go func() {
				defer wg.Done()
				for key := range rq {
					var val, _, err = sc.Get(key, 0)
					if err != nil {
						t.Errorf("source Get %s: %v", key.Hex()[:7], err)
						continue
					}
					if _, err = rc.SetWanted(key, val); err != nil {
						t.Errorf("SetWanted %s: %v", key.Hex()[:7], err)
					}
				}
			}()

			r.IsFull = true // a failed Run must reset it
			err = f.Run()
			f.Close()
			close(rq)
			wg.Wait()

			if !errors.Is(err, errDiskGone) {
				t.Fatalf("Run: want an error wrapping %v, got %v", errDiskGone, err)
			}
			if r.IsFull {
				t.Fatal("Run failed but left the Root marked full")
			}
		})
	}
}
