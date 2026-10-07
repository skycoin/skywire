package visor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func TestAutoTpCooldown(t *testing.T) {
	var c autoTpCooldown
	pk, _ := cipher.GenerateKeyPair()
	now := time.Now()
	require.False(t, c.blocked(pk, types.SUDPH, now))

	c.failed(pk, types.SUDPH, now)
	require.True(t, c.blocked(pk, types.SUDPH, now.Add(time.Minute)))
	require.False(t, c.blocked(pk, types.STCPR, now), "another type is tried")
	require.False(t, c.blocked(pk, types.SUDPH, now.Add(autoTpCooldownMin)))

	c.failed(pk, types.SUDPH, now)
	require.True(t, c.blocked(pk, types.SUDPH, now.Add(3*time.Minute)), "a second failure waits longer")
	for i := 0; i < 10; i++ {
		c.failed(pk, types.SUDPH, now)
	}
	require.False(t, c.blocked(pk, types.SUDPH, now.Add(autoTpCooldownMax)), "never longer than the cap")

	c.succeeded(pk, types.SUDPH)
	require.False(t, c.blocked(pk, types.SUDPH, now))
}
