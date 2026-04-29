package visor

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestForwardedPorts_IsWhitelisted(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	pk3, _ := cipher.GenerateKeyPair()

	t.Run("unregistered port denies", func(t *testing.T) {
		fp := NewForwardedPorts("")
		assert.False(t, fp.IsWhitelisted(8080, pk1))
	})

	t.Run("empty whitelist allows everyone", func(t *testing.T) {
		fp := NewForwardedPorts("")
		assert.NoError(t, fp.Register(ForwardedPort{Port: 8080, Skynet: true}))
		assert.True(t, fp.IsWhitelisted(8080, pk1))
		assert.True(t, fp.IsWhitelisted(8080, pk2))
	})

	t.Run("whitelist allows listed PKs only", func(t *testing.T) {
		fp := NewForwardedPorts("")
		assert.NoError(t, fp.Register(ForwardedPort{
			Port:      8080,
			Skynet:    true,
			Whitelist: []cipher.PubKey{pk1, pk2},
		}))
		assert.True(t, fp.IsWhitelisted(8080, pk1))
		assert.True(t, fp.IsWhitelisted(8080, pk2))
		assert.False(t, fp.IsWhitelisted(8080, pk3))
	})

	t.Run("clearing whitelist re-opens port", func(t *testing.T) {
		fp := NewForwardedPorts("")
		assert.NoError(t, fp.Register(ForwardedPort{
			Port:      8080,
			Skynet:    true,
			Whitelist: []cipher.PubKey{pk1},
		}))
		assert.False(t, fp.IsWhitelisted(8080, pk2))
		// Re-register with empty whitelist — the in-place update path
		// used by `cli serve whitelist <port> clear`.
		assert.NoError(t, fp.Register(ForwardedPort{Port: 8080, Skynet: true}))
		assert.True(t, fp.IsWhitelisted(8080, pk2))
	})
}
