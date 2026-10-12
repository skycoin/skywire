package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"sync"
	"testing"

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
