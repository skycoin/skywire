package dht

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

func signedItem(t *testing.T, pk cipher.PubKey, sk cipher.SecKey, seq uint64, value string, salt string) MutableItem {
	t.Helper()
	item := MutableItem{K: pk, Seq: seq, V: []byte(value), Salt: []byte(salt)}
	require.NoError(t, item.Sign(sk))
	return item
}

func TestStoreTrustTiers(t *testing.T) {
	whitelistedPK, whitelistedSK := cipher.GenerateKeyPair()
	trustedPK, trustedSK := cipher.GenerateKeyPair()
	publicPK, publicSK := cipher.GenerateKeyPair()

	store := NewStore(100, time.Hour)
	trust := NewTrustPolicy(
		[]cipher.PubKey{whitelistedPK},
		[]cipher.PubKey{trustedPK},
	)
	store.SetTrustPolicy(trust, 5, 3) // small pool for testing

	// All tiers can store items.
	require.NoError(t, store.Put(signedItem(t, whitelistedPK, whitelistedSK, 1, "wl", "s")))
	require.NoError(t, store.Put(signedItem(t, trustedPK, trustedSK, 1, "tr", "s")))
	require.NoError(t, store.Put(signedItem(t, publicPK, publicSK, 1, "pub", "s")))

	wl, tr, pub := store.CountByTier()
	assert.Equal(t, 1, wl)
	assert.Equal(t, 1, tr)
	assert.Equal(t, 1, pub)
}

func TestStoreWhitelistedNeverExpires(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	store := NewStore(100, 1*time.Millisecond) // very short TTL
	trust := NewTrustPolicy([]cipher.PubKey{pk}, nil)
	store.SetTrustPolicy(trust, 100, 50)

	require.NoError(t, store.Put(signedItem(t, pk, sk, 1, "permanent", "s")))

	time.Sleep(5 * time.Millisecond)

	// Whitelisted items survive TTL.
	got := store.Get(signedItem(t, pk, sk, 1, "permanent", "s").Target())
	require.NotNil(t, got, "whitelisted item should not expire")

	// Sweep should not remove whitelisted items.
	removed := store.ExpireSweep()
	assert.Equal(t, 0, removed)
}

func TestStorePublicExpires(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	store := NewStore(100, 1*time.Millisecond)

	require.NoError(t, store.Put(signedItem(t, pk, sk, 1, "temp", "s")))

	time.Sleep(5 * time.Millisecond)

	// Public items expire.
	got := store.Get(signedItem(t, pk, sk, 1, "temp", "s").Target())
	assert.Nil(t, got, "public item should expire")
}

func TestStorePublicPoolEviction(t *testing.T) {
	store := NewStore(100, time.Hour)
	store.SetTrustPolicy(NewTrustPolicy(nil, nil), 3, 100) // pool of 3

	// Fill pool with 4 items (exceeds pool of 3).
	for i := 0; i < 4; i++ {
		pk, sk := cipher.GenerateKeyPair()
		require.NoError(t, store.Put(signedItem(t, pk, sk, 1, "val", "s")))
	}

	// Should have evicted one public item.
	_, _, pub := store.CountByTier()
	assert.Equal(t, 3, pub, "public pool should not exceed capacity")
}

func TestStoreRateLimit(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	store := NewStore(100, time.Hour)
	store.SetTrustPolicy(NewTrustPolicy(nil, nil), 100, 2) // 2 items per PK

	// First two items should succeed.
	require.NoError(t, store.Put(signedItem(t, pk, sk, 1, "v1", "salt1")))
	require.NoError(t, store.Put(signedItem(t, pk, sk, 1, "v2", "salt2")))

	// Third item with different salt (new target) should be rate limited.
	assert.ErrorIs(t, store.Put(signedItem(t, pk, sk, 1, "v3", "salt3")), ErrRateLimited)

	// Updating an existing item (same target) should still work.
	require.NoError(t, store.Put(signedItem(t, pk, sk, 2, "v1-updated", "salt1")))
}

func TestStoreWhitelistedBypassesRateLimit(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	store := NewStore(100, time.Hour)
	trust := NewTrustPolicy([]cipher.PubKey{pk}, nil)
	store.SetTrustPolicy(trust, 100, 1) // 1 item per PK for public

	// Whitelisted PK should bypass rate limit.
	require.NoError(t, store.Put(signedItem(t, pk, sk, 1, "v1", "s1")))
	require.NoError(t, store.Put(signedItem(t, pk, sk, 1, "v2", "s2")))
	require.NoError(t, store.Put(signedItem(t, pk, sk, 1, "v3", "s3")))

	assert.Equal(t, 3, store.Len())
}

func TestStoreEvictionNeverTouchesWhitelisted(t *testing.T) {
	wlPK, wlSK := cipher.GenerateKeyPair()
	store := NewStore(3, time.Hour) // global cap of 3
	trust := NewTrustPolicy([]cipher.PubKey{wlPK}, nil)
	store.SetTrustPolicy(trust, 2, 100)

	// Add whitelisted item.
	require.NoError(t, store.Put(signedItem(t, wlPK, wlSK, 1, "wl", "s")))

	// Fill beyond capacity with public items.
	for i := 0; i < 5; i++ {
		pk, sk := cipher.GenerateKeyPair()
		_ = store.Put(signedItem(t, pk, sk, 1, "pub", "s")) //nolint:errcheck
	}

	// Whitelisted item should survive.
	got := store.Get(signedItem(t, wlPK, wlSK, 1, "wl", "s").Target())
	require.NotNil(t, got, "whitelisted item must survive eviction")
}
