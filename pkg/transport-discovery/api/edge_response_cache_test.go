package api

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestEdgeRespCache(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	t.Run("memoizes within TTL + gzip matches raw", func(t *testing.T) {
		c := newEdgeRespCache(time.Minute)
		var calls int32
		compute := func() ([]byte, error) {
			atomic.AddInt32(&calls, 1)
			return []byte(`[{"id":"x"}]`), nil
		}
		raw1, gz1, err := c.body(pkA, compute)
		require.NoError(t, err)
		raw2, gz2, err := c.body(pkA, compute)
		require.NoError(t, err)

		assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "second call within TTL must not recompute")
		assert.Equal(t, raw1, raw2)
		assert.Equal(t, gz1, gz2)
		assert.Equal(t, raw1, gunzip(t, gz1), "gzip body must decompress to the raw body")
	})

	t.Run("per-edge isolation", func(t *testing.T) {
		c := newEdgeRespCache(time.Minute)
		rawA, _, err := c.body(pkA, func() ([]byte, error) { return []byte(`["a"]`), nil })
		require.NoError(t, err)
		rawB, _, err := c.body(pkB, func() ([]byte, error) { return []byte(`["b"]`), nil })
		require.NoError(t, err)
		assert.NotEqual(t, rawA, rawB)
	})

	t.Run("stale-on-error returns last good body", func(t *testing.T) {
		short := newEdgeRespCache(100 * time.Millisecond)
		good := []byte(`["good"]`)
		_, _, err := short.body(pkA, func() ([]byte, error) { return good, nil })
		require.NoError(t, err)

		time.Sleep(150 * time.Millisecond) // expire the slot
		raw, _, err := short.body(pkA, func() ([]byte, error) { return nil, errors.New("store down") })
		require.NoError(t, err, "should serve stale, not error, when a prior body exists")
		assert.Equal(t, good, raw)
	})

	t.Run("error with no prior body propagates", func(t *testing.T) {
		fresh := newEdgeRespCache(time.Minute)
		_, _, err := fresh.body(pkA, func() ([]byte, error) { return nil, errors.New("store down") })
		require.Error(t, err)
	})

	t.Run("expired slots are swept on miss", func(t *testing.T) {
		// TTL deliberately well above the cost of two body() calls on a
		// slow CI runner. Earlier 20ms version flaked on linux runners
		// when the second body() call took >20ms to reach the sweep,
		// causing pkA to expire mid-test and the assert.Len(2) to fail.
		short := newEdgeRespCache(100 * time.Millisecond)
		// Populate two edges.
		_, _, err := short.body(pkA, func() ([]byte, error) { return []byte(`["a"]`), nil })
		require.NoError(t, err)
		_, _, err = short.body(pkB, func() ([]byte, error) { return []byte(`["b"]`), nil })
		require.NoError(t, err)
		require.Len(t, short.slots, 2)

		time.Sleep(150 * time.Millisecond) // expire both slots

		// One miss on pkA should sweep pkB's expired slot too, leaving only pkA.
		_, _, err = short.body(pkA, func() ([]byte, error) { return []byte(`["a2"]`), nil })
		require.NoError(t, err)
		assert.Len(t, short.slots, 1)
		_, hasB := short.slots[pkB]
		assert.False(t, hasB, "expired pkB slot should have been swept")
	})
}
