package api

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
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
