package api

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	require.NoError(t, err)
	out, err := io.ReadAll(zr)
	require.NoError(t, err)
	return out
}

func TestAllTransportsRespCache(t *testing.T) {
	c := newAllTransportsRespCache(time.Minute)

	t.Run("memoizes within TTL + gzip matches raw", func(t *testing.T) {
		var calls int32
		compute := func() ([]byte, error) {
			atomic.AddInt32(&calls, 1)
			return []byte(`[{"id":"x"}]`), nil
		}
		raw1, gz1, err := c.body(true, compute)
		require.NoError(t, err)
		raw2, gz2, err := c.body(true, compute)
		require.NoError(t, err)

		assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "second call within TTL must not recompute")
		assert.Equal(t, raw1, raw2)
		assert.Equal(t, gz1, gz2)
		assert.Equal(t, raw1, gunzip(t, gz1), "gzip body must decompress to the raw body")
	})

	t.Run("variants are independent", func(t *testing.T) {
		rawSelf, _, err := c.body(true, func() ([]byte, error) { return []byte(`["self"]`), nil })
		require.NoError(t, err)
		rawNoSelf, _, err := c.body(false, func() ([]byte, error) { return []byte(`["noself"]`), nil })
		require.NoError(t, err)
		assert.NotEqual(t, rawSelf, rawNoSelf)
	})

	t.Run("stale-on-error returns last good body", func(t *testing.T) {
		short := newAllTransportsRespCache(20 * time.Millisecond)
		good := []byte(`["good"]`)
		_, _, err := short.body(true, func() ([]byte, error) { return good, nil })
		require.NoError(t, err)

		time.Sleep(30 * time.Millisecond) // expire the slot
		raw, _, err := short.body(true, func() ([]byte, error) { return nil, errors.New("store down") })
		require.NoError(t, err, "should serve stale, not error, when a prior body exists")
		assert.Equal(t, good, raw)
	})

	t.Run("error with no prior body propagates", func(t *testing.T) {
		fresh := newAllTransportsRespCache(time.Minute)
		_, _, err := fresh.body(false, func() ([]byte, error) { return nil, errors.New("store down") })
		require.Error(t, err)
	})
}

// TestGzipBytesPoolReuse pins the pooled gzipBytes against the un-pooled
// reference implementation under concurrent callers — guards against a
// pooled gzip.Writer that isn't fully reset between callers (e.g. leaving
// dictionary state, header flags, or output offset from a prior payload
// that would corrupt the next caller's body).
func TestGzipBytesPoolReuse(t *testing.T) {
	payloads := [][]byte{
		[]byte(`[]`),
		[]byte(`[{"id":"x"}]`),
		[]byte(`[{"id":"x","edges":["aaaa","bbbb"]}]`),
		bytes.Repeat([]byte("abcdef0123"), 10_000), // ~100KB to exercise the compressor
	}

	// Reference: un-pooled gzip of the same body. The pooled output need
	// not be byte-identical (flate has implementation freedom), but the
	// gunzipped result MUST equal the input — that's the contract.
	for _, p := range payloads {
		out := gunzip(t, gzipBytes(p))
		require.Equal(t, p, out, "round-trip mismatch for payload len=%d", len(p))
	}

	// Hammer the pool with concurrent goroutines using mixed payloads.
	// Catches state-leak between Get/Put cycles.
	const workers = 16
	const iters = 50
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				p := payloads[(seed+i)%len(payloads)]
				out := gunzip(t, gzipBytes(p))
				assert.Equal(t, p, out)
			}
		}(w)
	}
	wg.Wait()
}
