// Package commands cmd/dmsg/pty-cli/commands/whitelist_test.go
//
// Covers pksFromArgs, the pure pubkey-parsing helper behind whitelist-add and
// whitelist-remove. The surrounding RunE closures call cli.WhitelistClient,
// which needs a live dmsgpty environment, so they are not unit-testable as-is.
package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestPksFromArgs covers parsing of zero, one, and several valid keys.
func TestPksFromArgs(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	t.Run("empty", func(t *testing.T) {
		pks, err := pksFromArgs(nil)
		require.NoError(t, err)
		assert.Empty(t, pks)
	})

	t.Run("single", func(t *testing.T) {
		pks, err := pksFromArgs([]string{pk1.Hex()})
		require.NoError(t, err)
		require.Len(t, pks, 1)
		assert.Equal(t, pk1, pks[0])
	})

	t.Run("multiple", func(t *testing.T) {
		pks, err := pksFromArgs([]string{pk1.Hex(), pk2.Hex()})
		require.NoError(t, err)
		assert.Equal(t, []cipher.PubKey{pk1, pk2}, pks)
	})
}

// TestPksFromArgsInvalid verifies a malformed key is reported with its index and
// aborts the whole parse.
func TestPksFromArgsInvalid(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	t.Run("first bad", func(t *testing.T) {
		_, err := pksFromArgs([]string{"not-a-key"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "index 0")
	})

	t.Run("second bad", func(t *testing.T) {
		pks, err := pksFromArgs([]string{pk.Hex(), "bad"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "index 1")
		assert.Nil(t, pks)
	})
}
