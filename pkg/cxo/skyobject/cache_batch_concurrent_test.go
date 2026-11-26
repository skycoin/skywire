package skyobject

import (
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
)

// Two WithBatch calls at once, as a publish and the cleanup sweep's DelRoot
// make them, take turns instead of each waiting on the other's tx.
func TestCacheWithBatchConcurrentCallers(t *testing.T) {
	conf := NewConfig()
	conf.DataDir = t.TempDir()
	conf.NoSyncCXDS = true
	cont, err := NewContainer(conf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cont.Close() }) //nolint:errcheck
	c := &cont.Cache

	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range 20 {
				err := c.WithBatch(func() error {
					for i := range 20 {
						val := make([]byte, 24)
						binary.LittleEndian.PutUint64(val, uint64(g))      //nolint:gosec
						binary.LittleEndian.PutUint64(val[8:], uint64(b))  //nolint:gosec
						binary.LittleEndian.PutUint64(val[16:], uint64(i)) //nolint:gosec
						if _, err := c.Set(cipher.SumSHA256(val), val, 1); err != nil {
							return err
						}
					}
					return nil
				})
				if err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent WithBatch callers did not finish")
	}
}
