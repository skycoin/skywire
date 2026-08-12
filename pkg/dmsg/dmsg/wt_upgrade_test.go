package dmsg

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestWTUpgradeBackoff covers the per-server backoff that keeps a browser on a
// working wss instead of re-dialing an unreachable WT endpoint every upgrade
// tick. The helpers only touch wtUpgradeFailAt/Mx, so a zero-value Client is
// enough.
func TestWTUpgradeBackoff(t *testing.T) {
	ce := &Client{}
	pk, _ := cipher.GenerateKeyPair()

	require.False(t, ce.wtUpgradeBackedOff(pk), "a never-tried server must not be backed off")

	ce.noteWTUpgradeFailure(pk)
	require.True(t, ce.wtUpgradeBackedOff(pk), "a just-failed server must be backed off")

	ce.clearWTUpgradeFailure(pk)
	require.False(t, ce.wtUpgradeBackedOff(pk), "a cleared server must not be backed off")

	// Distinct servers don't share backoff state.
	other, _ := cipher.GenerateKeyPair()
	ce.noteWTUpgradeFailure(pk)
	require.False(t, ce.wtUpgradeBackedOff(other), "backoff is per-server")
}
