// Package cliconfig cmd/skywire-cli/commands/config/regen_sk_test.go c0-cli
package cliconfig

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// regenKey mirrors the generator's choice on a regen: an explicitly supplied
// key wins, and only an unset one falls back to the identity already in the
// config.
func regenKey(supplied, old cipher.SecKey) cipher.SecKey {
	if supplied.Null() {
		return old
	}
	return supplied
}

// SK in skywire.conf, and --sk, must actually pin the visor's identity. Taking
// the old config's key unconditionally made both look like they did while
// silently doing nothing, because autoconfig regenerates on every run.
func TestRegenHonoursSuppliedKey(t *testing.T) {
	_, oldSK := cipher.GenerateKeyPair()
	_, newSK := cipher.GenerateKeyPair()

	require.Equal(t, newSK, regenKey(newSK, oldSK), "a supplied key changes the identity")

	var unset cipher.SecKey
	require.True(t, unset.Null())
	require.Equal(t, oldSK, regenKey(unset, oldSK), "unset keeps the existing identity")

	// The flag default is the all-zero key. Set rejects it outright, so a
	// missed env lookup leaves sk at its zero value, which is what the
	// fallback must key on.
	var zero cipher.SecKey
	require.Error(t, zero.Set("0000000000000000000000000000000000000000000000000000000000000000"),
		"the all-zero key is not a usable key")
	require.True(t, zero.Null())
	require.Equal(t, oldSK, regenKey(zero, oldSK))
}
