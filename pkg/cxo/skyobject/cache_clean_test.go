package skyobject

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
)

func cleanTestCache(tb testing.TB, amount int) *Cache {
	tb.Helper()
	conf := getTestConfig()
	conf.CacheMaxAmount = amount
	c, err := NewContainer(conf)
	if err != nil {
		tb.Fatal(err)
	}
	return &c.Cache
}

func fillCache(tb testing.TB, c *Cache, from, to int) {
	tb.Helper()
	for i := from; i < to; i++ {
		val := make([]byte, 40)
		binary.LittleEndian.PutUint64(val, uint64(i)) //nolint:gosec
		if _, err := c.Set(cipher.SumSHA256(val), val, 1); err != nil {
			tb.Fatal(err)
		}
	}
}

// Eviction keeps the cache within its limit and still finds what it holds.
func TestCacheCleanDownKeepsLimit(t *testing.T) {
	c := cleanTestCache(t, 100)
	fillCache(t, c, 0, 1000)
	c.mx.Lock()
	n := len(c.is)
	c.mx.Unlock()
	if n == 0 || n > 100 {
		t.Fatalf("cache holds %d items, limit 100", n)
	}
	for _, it := range c.rankBuf[:cap(c.rankBuf)] {
		if it.it != nil {
			t.Fatal("rank buffer keeps item pointers")
		}
	}
}

func BenchmarkCacheCleanDown(b *testing.B) {
	c := cleanTestCache(b, 20000)
	fillCache(b, c, 0, 20000)
	b.ReportAllocs()
	b.ResetTimer()
	fillCache(b, c, 20000, 20000+b.N)
}

// Evicting items with pending rc changes writes all of them in one
// transaction. One batched Inc each waited out bbolt's batch delay in turn,
// about 10 ms per item, while holding the cache lock.
func TestCacheCleanDownWritesPendingRCInOneBatch(t *testing.T) {
	conf := NewConfig()
	conf.DataDir = t.TempDir()
	conf.NoSyncCXDS = true
	conf.CacheMaxAmount = 400
	cont, err := NewContainer(conf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cont.Close() }) //nolint:errcheck
	c := &cont.Cache

	keys := make([]cipher.SHA256, 0, 400)
	for i := range 400 {
		val := make([]byte, 40)
		binary.LittleEndian.PutUint64(val, uint64(i)) //nolint:gosec
		key := cipher.SumSHA256(val)
		if _, err := c.Set(key, val, 1); err != nil {
			t.Fatal(err)
		}
		if _, _, err := c.Get(key, 1); err != nil { // pending in the cache only
			t.Fatal(err)
		}
		keys = append(keys, key)
	}

	start := time.Now()
	fillCache(t, c, 1000, 1001) // one more evicts about a fifth
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("eviction took %s", d)
	}
	c.mx.Lock()
	var evicted []cipher.SHA256
	for _, k := range keys {
		if _, ok := c.is[k]; !ok {
			evicted = append(evicted, k)
		}
	}
	c.mx.Unlock()
	if len(evicted) < 50 {
		t.Fatalf("only %d items evicted", len(evicted))
	}
	for _, k := range evicted {
		_, rc, err := c.db().Get(k, 0)
		if err != nil {
			t.Fatal(err)
		}
		if rc != 2 {
			t.Fatalf("evicted item has rc %d in the store, want 2", rc)
		}
	}
}
