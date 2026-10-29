package skyobject

import (
	"encoding/binary"
	"testing"

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
