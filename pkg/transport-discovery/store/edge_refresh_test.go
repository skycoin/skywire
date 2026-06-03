package store

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestEdgeRefreshTargets verifies the per-edge-TTL decision: a registration
// refreshes only the reporting edge's index, so an offline edge's index expires
// while its live peer keeps re-registering the shared transport. A reporter
// that is not an edge (zero PK / legacy / unauthenticated) falls back to both.
func TestEdgeRefreshTargets(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	edges := [2]cipher.PubKey{a, b}

	t.Run("reporter is edge A -> only A", func(t *testing.T) {
		ra, rb := edgeRefreshTargets(a, edges)
		assert.True(t, ra)
		assert.False(t, rb)
	})
	t.Run("reporter is edge B -> only B", func(t *testing.T) {
		ra, rb := edgeRefreshTargets(b, edges)
		assert.False(t, ra)
		assert.True(t, rb)
	})
	t.Run("reporter is neither (other PK) -> both (fallback)", func(t *testing.T) {
		ra, rb := edgeRefreshTargets(other, edges)
		assert.True(t, ra)
		assert.True(t, rb)
	})
	t.Run("reporter is zero PK -> both (fallback)", func(t *testing.T) {
		ra, rb := edgeRefreshTargets(cipher.PubKey{}, edges)
		assert.True(t, ra)
		assert.True(t, rb)
	})
}
