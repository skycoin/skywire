package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

func mustKey(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

func TestEntryCache_GetMissSetHit(t *testing.T) {
	c := newEntryCache(4, time.Minute)
	pk := mustKey(t)

	_, ok := c.get(pk)
	require.False(t, ok, "empty cache must miss")

	e := &disc.Entry{Static: pk}
	c.set(pk, e)

	got, ok := c.get(pk)
	require.True(t, ok, "set-then-get must hit")
	assert.Same(t, e, got)
}

func TestEntryCache_Expiry(t *testing.T) {
	c := newEntryCache(4, 20*time.Millisecond)
	pk := mustKey(t)
	c.set(pk, &disc.Entry{Static: pk})

	_, ok := c.get(pk)
	require.True(t, ok)

	time.Sleep(40 * time.Millisecond)
	_, ok = c.get(pk)
	assert.False(t, ok, "entry past TTL must miss")
}

func TestEntryCache_Invalidate(t *testing.T) {
	c := newEntryCache(4, time.Minute)
	pk := mustKey(t)
	c.set(pk, &disc.Entry{Static: pk})

	c.invalidate(pk)

	_, ok := c.get(pk)
	assert.False(t, ok, "invalidate must drop the entry")
}

func TestEntryCache_NilNotStored(t *testing.T) {
	c := newEntryCache(4, time.Minute)
	pk := mustKey(t)
	c.set(pk, nil)

	_, ok := c.get(pk)
	assert.False(t, ok, "nil entry must not be cached")
}

func TestEntryCache_CapacityEvictsExpiredFirst(t *testing.T) {
	c := newEntryCache(3, 20*time.Millisecond)
	var keys [4]cipher.PubKey
	for i := range keys {
		keys[i] = mustKey(t)
	}

	c.set(keys[0], &disc.Entry{Static: keys[0]})
	c.set(keys[1], &disc.Entry{Static: keys[1]})
	c.set(keys[2], &disc.Entry{Static: keys[2]})
	require.Len(t, c.items, 3)

	time.Sleep(40 * time.Millisecond)

	c.set(keys[3], &disc.Entry{Static: keys[3]})

	// The three expired entries should have been evicted; only keys[3] remains.
	assert.Len(t, c.items, 1)
	_, ok := c.get(keys[3])
	assert.True(t, ok)
}

func TestEntryCache_CapacityEvictsWhenNoneExpired(t *testing.T) {
	c := newEntryCache(2, time.Minute)
	var keys [3]cipher.PubKey
	for i := range keys {
		keys[i] = mustKey(t)
	}

	c.set(keys[0], &disc.Entry{Static: keys[0]})
	c.set(keys[1], &disc.Entry{Static: keys[1]})
	c.set(keys[2], &disc.Entry{Static: keys[2]}) // forces eviction

	assert.LessOrEqual(t, len(c.items), 2, "cache must stay within capacity")
	_, ok := c.get(keys[2])
	assert.True(t, ok, "freshly-set entry must be present after eviction")
}

func TestEntryCache_OverwriteDoesNotEvict(t *testing.T) {
	c := newEntryCache(2, time.Minute)
	pk1 := mustKey(t)
	pk2 := mustKey(t)
	c.set(pk1, &disc.Entry{Static: pk1})
	c.set(pk2, &disc.Entry{Static: pk2})

	// Overwriting an existing key when at capacity must NOT evict a different entry.
	updated := &disc.Entry{Static: pk1}
	c.set(pk1, updated)

	got, ok := c.get(pk2)
	require.True(t, ok)
	assert.Equal(t, pk2, got.Static)

	got, ok = c.get(pk1)
	require.True(t, ok)
	assert.Same(t, updated, got)
}
