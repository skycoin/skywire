package dmsg

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestCarrierConvergeBackoff covers the per-server backoff that keeps a browser on a
// working wss instead of re-dialing an unreachable preferred endpoint every converge
// tick. The helpers only touch carrierFailAt/Mx, so a zero-value Client is
// enough.
func TestCarrierConvergeBackoff(t *testing.T) {
	ce := &Client{}
	pk, _ := cipher.GenerateKeyPair()

	require.False(t, ce.carrierBackedOff(pk), "a never-tried server must not be backed off")

	ce.noteCarrierFailure(pk, "")
	require.True(t, ce.carrierBackedOff(pk), "a just-failed server must be backed off")

	ce.clearCarrierFailure(pk)
	require.False(t, ce.carrierBackedOff(pk), "a cleared server must not be backed off")

	// Distinct servers don't share backoff state.
	other, _ := cipher.GenerateKeyPair()
	ce.noteCarrierFailure(pk, "")
	require.False(t, ce.carrierBackedOff(other), "backoff is per-server")
}
