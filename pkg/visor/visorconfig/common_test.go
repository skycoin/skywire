package visorconfig

import (
	"testing"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// When 'ensureKeys' is triggered, a 'Common' struct with:
// - No keys defined SHOULD generate a random valid key pair.
// - Both keys defined SHOULD make no changes.
// - Just the secret key defined SHOULD generate the public key.
func TestCommon_ensureKeys(t *testing.T) {

	t.Run("no_keys", func(t *testing.T) {
		// init
		cc, err := NewCommon(nil, "", "", nil)
		require.NoError(t, err)

		// test
		require.NoError(t, cc.ensureKeys())

		// check
		assert.False(t, cc.pk.Null())
		assert.False(t, cc.SK.Null())
		pk, err := cc.SK.PubKey()
		assert.NoError(t, err)
		assert.Equal(t, cc.pk, pk)
	})

	t.Run("both_keys", func(t *testing.T) {
		// init
		cc, err := NewCommon(nil, "", "", nil)
		require.NoError(t, err)

		// init: expected key pair (this should not change)
		pk, sk := cipher.GenerateKeyPair()
		cc.pk = pk
		cc.SK = sk

		// test
		require.NoError(t, cc.ensureKeys())

		// check
		assert.Equal(t, pk, cc.pk)
		assert.Equal(t, sk, cc.SK)
	})

	t.Run("only_secret_key", func(t *testing.T) {
		// init
		cc, err := NewCommon(nil, "", "", nil)
		require.NoError(t, err)

		// init: expected key pair
		pk, sk := cipher.GenerateKeyPair()
		cc.SK = sk

		// test
		require.NoError(t, cc.ensureKeys())

		// check
		assert.Equal(t, pk, cc.pk)
		assert.Equal(t, sk, cc.SK)
	})
}
