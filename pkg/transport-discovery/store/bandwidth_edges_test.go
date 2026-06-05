//go:build !no_ci
// +build !no_ci

package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestParseBandwidthEdgePair covers the round-trip of the value written by
// bandwidthEdgesKey at registration and read back in recoverBandwidthEdges —
// the mechanism that keeps an expired transport's counterparty identifiable
// even when only one edge ever published bandwidth.
func TestParseBandwidthEdgePair(t *testing.T) {
	pk0, _ := cipher.GenerateKeyPair()
	pk1, _ := cipher.GenerateKeyPair()

	t.Run("valid pair round-trips in order", func(t *testing.T) {
		edges, ok := parseBandwidthEdgePair(pk0.Hex() + "," + pk1.Hex())
		require.True(t, ok)
		assert.Equal(t, pk0, edges[0])
		assert.Equal(t, pk1, edges[1])
	})

	t.Run("rejects wrong field count", func(t *testing.T) {
		_, ok := parseBandwidthEdgePair(pk0.Hex())
		assert.False(t, ok)
		_, ok = parseBandwidthEdgePair(pk0.Hex() + "," + pk1.Hex() + "," + pk0.Hex())
		assert.False(t, ok)
	})

	t.Run("rejects malformed hex", func(t *testing.T) {
		_, ok := parseBandwidthEdgePair("not-a-pk," + pk1.Hex())
		assert.False(t, ok)
	})

	t.Run("rejects empty", func(t *testing.T) {
		_, ok := parseBandwidthEdgePair("")
		assert.False(t, ok)
	})
}
